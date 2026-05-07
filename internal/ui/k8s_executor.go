package ui

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/k8sdebug"
)

type k8sNativeClient struct {
	config    *rest.Config
	clientset kubernetes.Interface
	namespace string
	podName   string
	container string
}

func (c *k8sNativeClient) Close() error {
	return nil // no persistent connection to close, SPDY connections are ephemeral per exec
}

func (c *k8sNativeClient) execInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer, tty bool, sizeQ remotecommand.TerminalSizeQueue) error {
	req := c.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(c.podName).
		Namespace(c.namespace).
		SubResource("exec")

	opts := &corev1.PodExecOptions{
		Command: cmd,
		Stdin:   stdin != nil,
		Stdout:  stdout != nil,
		Stderr:  stderr != nil,
		TTY:     tty,
	}
	if c.container != "" {
		opts.Container = c.container
	}
	req.VersionedParams(opts, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("create spdy executor: %w", err)
	}

	streamOpts := remotecommand.StreamOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Tty:    tty,
	}
	if tty && sizeQ != nil {
		streamOpts.TerminalSizeQueue = sizeQ
	}
	err = exec.StreamWithContext(ctx, streamOpts)
	if err != nil {
		return fmt.Errorf("exec stream: %w", err)
	}

	return nil
}

func (c *k8sNativeClient) Run(cmd string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	err := c.execInPod(context.Background(), []string{"sh", "-c", cmd}, nil, &stdout, &stderr, false, nil)
	if err != nil {
		if stderr.Len() > 0 {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, stderr.String())
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func (c *k8sNativeClient) Upload(localPath, remotePath string) error {
	localFile, err := os.Open(localPath) // #nosec G304 -- CLI tool, user explicitly provides the local path for upload
	if err != nil {
		return err
	}
	defer localFile.Close()

	stat, err := localFile.Stat()
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()

	go func() {
		defer pw.Close()
		tw := tar.NewWriter(pw)
		defer tw.Close()

		hdr := &tar.Header{
			Name: filepath.Base(remotePath),
			Mode: int64(stat.Mode()),
			Size: stat.Size(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return
		}
		_, _ = io.Copy(tw, localFile)
	}()

	remoteDir := filepath.Dir(remotePath)
	var stderr bytes.Buffer
	// Create the directory if it doesn't exist, then extract the tar stream into it
	cmd := []string{"sh", "-c", fmt.Sprintf("mkdir -p '%s' && tar -xf - -C '%s'", remoteDir, remoteDir)}

	if err := c.execInPod(context.Background(), cmd, pr, nil, &stderr, false, nil); err != nil {
		return fmt.Errorf("upload extract failed: %w: %s", err, stderr.String())
	}

	return nil
}

func (c *k8sNativeClient) Download(remotePath, localPath string) error {
	remoteDir := filepath.Dir(remotePath)
	remoteBase := filepath.Base(remotePath)

	pr, pw := io.Pipe()
	var stderr bytes.Buffer

	go func() {
		defer pw.Close()
		cmd := []string{"tar", "-cf", "-", "-C", remoteDir, remoteBase}
		_ = c.execInPod(context.Background(), cmd, nil, pw, &stderr, false, nil)
	}()

	tr := tar.NewReader(pr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break // End of archive
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		if hdr.Name == remoteBase {
			// #nosec G304 -- CLI tool, user explicitly provides the local path for download
			localFile, err := os.OpenFile(localPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, hdr.FileInfo().Mode())
			if err != nil {
				return err
			}
			defer localFile.Close()

			// #nosec G110 -- decompression bounded by the user's pod, intentionally downloading
			if _, err := io.Copy(localFile, tr); err != nil {
				return err
			}
			return nil // We found and extracted our file
		}
	}

	if stderr.Len() > 0 {
		return fmt.Errorf("download failed: %s", stderr.String())
	}
	return fmt.Errorf("file not found in remote archive")
}

func (k k8sPodExecutor) Dial(_ string, r hosts.Record) (HostClient, error) {
	zap.L().Debug("dialing k8s pod executor", zap.String("record", r.Name))
	namespace := r.Meta["namespace"]
	podName := r.Meta["pod_name"]
	kubeContext := r.Meta["kube_context"]
	kubeconfig := r.Meta["kubeconfig"]

	if namespace == "" || podName == "" {
		return nil, fmt.Errorf("missing k8s namespace or pod_name in metadata")
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)

	config, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("k8s client: %w", err)
	}

	debugImage := r.Meta["debug_image"]
	containerName, err := k8sdebug.EnsureEphemeralContainer(context.Background(), clientset, namespace, podName, debugImage)
	if err != nil {
		return nil, fmt.Errorf("ensure ephemeral container: %w", err)
	}

	return &k8sNativeClient{
		config:    config,
		clientset: clientset,
		namespace: namespace,
		podName:   podName,
		container: containerName,
	}, nil
}

func (k k8sPodExecutor) RunInteractive(user string, r hosts.Record) error {
	return runK8sInteractiveWithRecorder(user, r, nil)
}

func runK8sInteractiveWithRecorder(user string, r hosts.Record, recorder *SessionRecorder) error {
	client, err := k8sPodExecutor{}.Dial(user, r)
	if err != nil {
		recorder.RecordError(err)
		return err
	}
	defer func() { _ = client.Close() }()

	podClient, ok := client.(*k8sNativeClient)
	if !ok {
		err := fmt.Errorf("unexpected client type %T", client)
		recorder.RecordError(err)
		return err
	}

	fd := int(os.Stdin.Fd())
	if !termIsTerminal(fd) {
		err := fmt.Errorf("stdin is not a terminal")
		recorder.RecordError(err)
		return err
	}
	oldState, err := termMakeRaw(fd)
	if err != nil {
		recorder.RecordError(err)
		return err
	}
	defer func() { _ = termRestore(fd, oldState) }()

	// Inject host environment variables into the interactive shell session
	env, _ := cuetry.EffectiveEnvForRun(cuetry.RecipeStep{}, nil, nil, &r)
	cmd, _ := cuetry.ShellExportPrefixForRemote(env, "sh")

	// Start standard sh for interactive session
	stdin := WrapRecordingReader(os.Stdin, recorder, "stdin")
	stdout := WrapRecordingWriter(os.Stdout, recorder, "stdout")
	stderr := WrapRecordingWriter(os.Stderr, recorder, "stderr")
	execErr := podClient.execInPod(context.Background(), []string{"sh", "-c", cmd}, stdin, stdout, stderr, true, nil)
	if execErr != nil {
		recorder.RecordError(execErr)
	}
	return execErr
}
