package k8sprovider

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"

	//nolint:staticcheck // SA1019: required by client-go v0.36.1 spdy.NewDialer interface
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/transport/spdy"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/k8sdebug"
)

// k8sPortForwardRequestID provides unique request IDs for concurrent DialUpstream calls.
var k8sPortForwardRequestID atomic.Uint32

// InteractiveRunner runs an interactive TTY session against a Kubernetes pod.
// It is implemented in the ui package and injected via NewFactory to keep
// k8sprovider a leaf package (ui imports k8sprovider, not vice versa).
type InteractiveRunner interface {
	RunInteractive(user string, r hosts.Record) error
}

// K8sNativeClient implements hostexec.HostClient via kubectl exec / SPDY streaming.
type K8sNativeClient struct {
	Config    *rest.Config
	Clientset kubernetes.Interface
	Namespace string
	PodName   string
	Container string
}

// K8sPodExecutor implements hostexec.Executor for Kubernetes pods.
// interactive is injected by the factory for resolver-created executors; it is
// nil for executors built ad-hoc only to Dial (which never call RunInteractive).
type K8sPodExecutor struct {
	interactive InteractiveRunner
}

// k8sClientConfigFromRecord builds a *rest.Config from kubeconfig/context stored in r.Meta.
func k8sClientConfigFromRecord(r hosts.Record) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kc := r.Meta["kubeconfig"]; kc != "" {
		loadingRules.ExplicitPath = kc
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kctx := r.Meta["kube_context"]; kctx != "" {
		overrides.CurrentContext = kctx
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
}

// summarizeK8sExecCmd returns a short preview of argv for debug logs (avoids huge sh -c bodies).
func summarizeK8sExecCmd(cmd []string) string {
	if len(cmd) == 0 {
		return "(empty)"
	}
	const maxPreview = 512
	s := strings.Join(cmd, " ")
	if len(s) <= maxPreview {
		return s
	}
	return s[:maxPreview] + "..."
}

// Close is a no-op; SPDY connections are ephemeral per exec.
func (c *K8sNativeClient) Close() error {
	return nil // no persistent connection to close, SPDY connections are ephemeral per exec
}

// ExecInPod runs cmd in the pod with the provided streams and optional terminal size queue.
func (c *K8sNativeClient) ExecInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer, tty bool, sizeQ remotecommand.TerminalSizeQueue) error {
	logMeta := []zap.Field{
		zap.String("k8s_namespace", c.Namespace),
		zap.String("k8s_pod", c.PodName),
		zap.String("k8s_container", c.Container),
		zap.Int("command_argc", len(cmd)),
		zap.String("command_preview", summarizeK8sExecCmd(cmd)),
		zap.Bool("stdin", stdin != nil),
		zap.Bool("stdout", stdout != nil),
		zap.Bool("stderr", stderr != nil),
		zap.Bool("tty", tty),
		zap.Bool("terminal_size_queue", sizeQ != nil),
	}
	zap.L().Debug("k8s execInPod: starting", logMeta...)

	req := c.Clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(c.PodName).
		Namespace(c.Namespace).
		SubResource("exec")

	opts := &corev1.PodExecOptions{
		Command: cmd,
		Stdin:   stdin != nil,
		Stdout:  stdout != nil,
		Stderr:  stderr != nil,
		TTY:     tty,
	}
	if c.Container != "" {
		opts.Container = c.Container
	}
	req.VersionedParams(opts, scheme.ParameterCodec)

	u := req.URL()
	zap.L().Debug("k8s execInPod: exec subresource URL", append(append([]zap.Field(nil), logMeta...), zap.String("url_path", u.Path))...)

	exec, err := remotecommand.NewSPDYExecutor(c.Config, "POST", u)
	if err != nil {
		zap.L().Debug("k8s execInPod: new SPDY executor failed", append(append([]zap.Field(nil), logMeta...), zap.Error(err))...)
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
	zap.L().Debug("k8s execInPod: streaming", logMeta...)
	err = exec.StreamWithContext(ctx, streamOpts)
	if err != nil {
		zap.L().Debug("k8s execInPod: stream finished with error", append(append([]zap.Field(nil), logMeta...), zap.Error(err))...)
		return fmt.Errorf("exec stream: %w", err)
	}
	zap.L().Debug("k8s execInPod: stream finished ok", logMeta...)
	return nil
}

// Run executes cmd in the pod via sh -c and returns combined stdout.
func (c *K8sNativeClient) Run(cmd string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	err := c.ExecInPod(context.Background(), []string{"sh", "-c", cmd}, nil, &stdout, &stderr, false, nil)
	if err != nil {
		if stderr.Len() > 0 {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, stderr.String())
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// RunWithStreams executes cmd in the pod with the provided I/O streams.
func (c *K8sNativeClient) RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	if stderr == nil {
		stderr = io.Discard
	}
	return c.ExecInPod(context.Background(), []string{"sh", "-c", cmd}, stdin, stdout, stderr, false, nil)
}

// Upload copies a local file into the pod at remotePath via tar exec.
func (c *K8sNativeClient) Upload(localPath, remotePath string) error {
	localPath = strings.TrimSpace(localPath)
	remotePath = strings.TrimSpace(remotePath)
	if localPath == "" || remotePath == "" {
		return fmt.Errorf("upload: empty local or remote path")
	}
	// Trailing slash means "directory" (same as SFTP): use the local file's base name in the pod.
	if strings.HasSuffix(remotePath, "/") {
		base := filepath.Base(localPath)
		if base == "." || base == ".." || base == "/" || base == "" {
			return fmt.Errorf("upload: need a file name inside %q (local path has no usable base name)", remotePath)
		}
		remotePath = path.Join(strings.TrimRight(remotePath, "/"), base)
	}

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
			Name: path.Base(remotePath),
			Mode: int64(stat.Mode()),
			Size: stat.Size(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return
		}
		_, _ = io.Copy(tw, localFile)
	}()

	remoteDir := path.Dir(remotePath)
	var stderr bytes.Buffer
	// Create the directory if it doesn't exist, then extract the tar stream into it
	cmd := []string{"sh", "-c", fmt.Sprintf("mkdir -p '%s' && tar -xf - -C '%s'", remoteDir, remoteDir)}

	if err := c.ExecInPod(context.Background(), cmd, pr, nil, &stderr, false, nil); err != nil {
		return fmt.Errorf("upload extract failed: %w: %s", err, stderr.String())
	}

	return nil
}

// Download copies a file from the pod to localPath via tar exec.
func (c *K8sNativeClient) Download(remotePath, localPath string) error {
	remoteDir := filepath.Dir(remotePath)
	remoteBase := filepath.Base(remotePath)

	pr, pw := io.Pipe()
	var stderr bytes.Buffer

	go func() {
		defer pw.Close()
		cmd := []string{"tar", "-cf", "-", "-C", remoteDir, remoteBase}
		_ = c.ExecInPod(context.Background(), cmd, nil, pw, &stderr, false, nil)
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

// ListRemoteDir is not supported for k8s pods.
func (c *K8sNativeClient) ListRemoteDir(_ string) ([]hostexec.RemoteFileEntry, error) {
	return nil, fmt.Errorf("k8s pod file listing is not supported in this view")
}

// StatRemote is not supported for k8s pods.
func (c *K8sNativeClient) StatRemote(_ string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, fmt.Errorf("k8s pod file stat is not supported in this view")
}

// MkdirAllRemote is not supported for k8s pods.
func (c *K8sNativeClient) MkdirAllRemote(_ string) error {
	return fmt.Errorf("k8s pod directory create is not supported in this view")
}

// RemoveRemote is not supported for k8s pods.
func (c *K8sNativeClient) RemoveRemote(_ string, _ bool) error {
	return fmt.Errorf("k8s pod file remove is not supported in this view")
}

// Dial connects to the k8s pod, creating an ephemeral debug container if needed.
func (k *K8sPodExecutor) Dial(_ string, r hosts.Record) (hostexec.HostClient, error) {
	zap.L().Debug("dialing k8s pod executor", zap.String("record", r.Name))
	namespace := r.Meta["namespace"]
	podName := r.Meta["pod_name"]

	if namespace == "" || podName == "" {
		return nil, fmt.Errorf("missing k8s namespace or pod_name in metadata")
	}

	config, err := k8sClientConfigFromRecord(r)
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

	return &K8sNativeClient{
		Config:    config,
		Clientset: clientset,
		Namespace: namespace,
		PodName:   podName,
		Container: containerName,
	}, nil
}

// RunInteractive delegates to the injected InteractiveRunner.
func (k *K8sPodExecutor) RunInteractive(user string, r hosts.Record) error {
	if k.interactive == nil {
		return fmt.Errorf("k8s interactive session not configured")
	}
	return k.interactive.RunInteractive(user, r)
}

// RunTunnel performs k8s port-forwarding via the SPDY API.
func (k *K8sPodExecutor) RunTunnel(ctx context.Context, _ string, r hosts.Record, localFwd string, out io.Writer) error {
	namespace := r.Meta["namespace"]
	podName := r.Meta["pod_name"]

	if namespace == "" || podName == "" {
		return fmt.Errorf("missing k8s namespace or pod_name in metadata")
	}

	cfg, err := k8sClientConfigFromRecord(r)
	if err != nil {
		return fmt.Errorf("k8s config: %w", err)
	}

	// Parse localFwd format "localPort:remotePort" (in SSH it's localPort:remoteHost:remotePort, but in k8s we only forward to the pod itself)
	// We'll normalize it for k8s port-forward format: "local:remote" or just "port"
	parts := strings.Split(localFwd, ":")
	var ports []string
	switch len(parts) {
	case 3:
		// e.g. "8080:localhost:80" -> use "8080:80"
		ports = []string{fmt.Sprintf("%s:%s", parts[0], parts[2])}
	default:
		// e.g. "8080:80" or "8080"
		ports = []string{localFwd}
	}

	reqURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", cfg.Host, namespace, podName))
	if err != nil {
		return err
	}

	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return fmt.Errorf("spdy round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)

	stopCh := make(chan struct{}, 1)
	readyCh := make(chan struct{})

	// Handle cancellation via context
	go func() {
		<-ctx.Done()
		close(stopCh)
	}()

	fw, err := portforward.New(dialer, ports, stopCh, readyCh, out, out)
	if err != nil {
		return fmt.Errorf("create port forwarder: %w", err)
	}

	fmt.Fprintf(out, "\r\n[honey] Forwarding %s -> Pod %s in namespace %s (Ctrl+C to stop)\n", strings.Join(ports, ", "), podName, namespace)
	return fw.ForwardPorts()
}

// DialUpstream connects to a port inside the pod via k8s port-forward.
func (k *K8sPodExecutor) DialUpstream(_ context.Context, _ string, r hosts.Record, address string) (net.Conn, error) {
	namespace := r.Meta["namespace"]
	podName := r.Meta["pod_name"]

	if namespace == "" || podName == "" {
		return nil, fmt.Errorf("missing k8s namespace or pod_name in metadata")
	}

	cfg, err := k8sClientConfigFromRecord(r)
	if err != nil {
		return nil, fmt.Errorf("k8s config: %w", err)
	}

	_, portStr, err := net.SplitHostPort(address)
	if err != nil {
		portStr = address
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid port %s", portStr)
	}

	reqURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", cfg.Host, namespace, podName))
	if err != nil {
		return nil, err
	}

	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("spdy round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", reqURL)

	streamConn, _, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
	if err != nil {
		return nil, err
	}

	reqID := strconv.FormatUint(uint64(k8sPortForwardRequestID.Add(1)), 10)
	headers := http.Header{}
	headers.Set("streamType", "error")
	headers.Set("port", strconv.Itoa(port))
	headers.Set("requestID", reqID)

	errorStream, err := streamConn.CreateStream(headers)
	if err != nil {
		_ = streamConn.Close()
		return nil, err
	}
	_ = errorStream.Close()

	headers.Set("streamType", "data")
	dataStream, err := streamConn.CreateStream(headers)
	if err != nil {
		_ = streamConn.Close()
		return nil, err
	}

	return &k8sStreamConn{
		Stream: dataStream,
		conn:   streamConn,
	}, nil
}

type k8sStreamConn struct {
	httpstream.Stream
	conn httpstream.Connection
}

func (c *k8sStreamConn) Close() error {
	_ = c.Stream.Close()
	return c.conn.Close()
}

func (c *k8sStreamConn) LocalAddr() net.Addr                { return &net.TCPAddr{} }
func (c *k8sStreamConn) RemoteAddr() net.Addr               { return &net.TCPAddr{} }
func (c *k8sStreamConn) SetDeadline(_ time.Time) error      { return nil }
func (c *k8sStreamConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *k8sStreamConn) SetWriteDeadline(_ time.Time) error { return nil }

// StartLocalForward starts a local port forward.
func (c *K8sNativeClient) StartLocalForward(_ context.Context, _ string, _ int, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartRemoteForward starts a remote port forward.
func (c *K8sNativeClient) StartRemoteForward(_ context.Context, _ string, _ int, _ string, _ int) (remAddr string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartDynamicForward starts a dynamic port forward.
func (c *K8sNativeClient) StartDynamicForward(_ context.Context, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartUDPRelay starts a UDP relay.
func (c *K8sNativeClient) StartUDPRelay(_ context.Context, _ string, _ int, _ string, _ int, _ bool) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartTunForward starts a TUN forward.
func (c *K8sNativeClient) StartTunForward(_ context.Context, _ string, _ string, _ int, _, _ int) (tunName string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}
