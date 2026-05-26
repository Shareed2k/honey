package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
)

// LogOptions controls distributed log streaming.
type LogOptions struct {
	Target         string
	Source         string
	Follow         bool
	Tail           int64
	Since          time.Duration
	Timestamps     bool
	Container      string
	Unit           string
	Command        string
	RunAs          string
	MaxConcurrency int
}

// StreamLogs streams logs for records to out with stable per-record prefixes.
func StreamLogs(ctx context.Context, user string, records []hosts.Record, opts LogOptions, cache *ClientCache, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if opts.Tail <= 0 {
		opts.Tail = 100
	}
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = 8
	}

	g, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, opts.MaxConcurrency)
	var writeMu sync.Mutex
	for _, rec := range records {
		rec := rec
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return ctx.Err()
			}
			return streamOneLog(ctx, user, rec, opts, cache, out, &writeMu)
		})
	}
	return g.Wait()
}

func streamOneLog(ctx context.Context, user string, rec hosts.Record, opts LogOptions, cache *ClientCache, out io.Writer, mu *sync.Mutex) error {
	prefix := logPrefix(rec)
	if opts.Command != "" || opts.Unit != "" || strings.TrimSpace(opts.Source) != "" {
		return streamExecutorLogs(ctx, user, rec, opts, cache, out, mu, prefix)
	}

	switch {
	case rec.Provider == "k8s" && rec.Meta["kind"] == "pod":
		return streamK8sPodLogs(ctx, rec, opts, out, mu, prefix)
	case rec.Provider == "docker" && (rec.Meta["kind"] == "container" || rec.Meta["kind"] == "swarm_task"):
		return streamDockerLogs(ctx, user, rec, opts, out, mu, prefix)
	default:
		return streamExecutorLogs(ctx, user, rec, opts, cache, out, mu, prefix)
	}
}

func streamK8sPodLogs(ctx context.Context, rec hosts.Record, opts LogOptions, out io.Writer, mu *sync.Mutex, prefix string) error {
	namespace := strings.TrimSpace(rec.Meta["namespace"])
	podName := strings.TrimSpace(rec.Meta["pod_name"])
	if namespace == "" || podName == "" {
		return fmt.Errorf("%s missing k8s namespace or pod_name", rec.Name)
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig := strings.TrimSpace(rec.Meta["kubeconfig"]); kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if kubeContext := strings.TrimSpace(rec.Meta["kube_context"]); kubeContext != "" {
		overrides.CurrentContext = kubeContext
	}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	cfg, err := cc.ClientConfig()
	if err != nil {
		return err
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}

	logOpts := &corev1.PodLogOptions{
		Container:  strings.TrimSpace(opts.Container),
		Follow:     opts.Follow,
		Timestamps: opts.Timestamps,
	}
	if opts.Tail > 0 {
		logOpts.TailLines = &opts.Tail
	}
	if opts.Since > 0 {
		secs := int64(opts.Since.Seconds())
		if secs > 0 {
			logOpts.SinceSeconds = &secs
		}
	}

	r, err := clientset.CoreV1().Pods(namespace).GetLogs(podName, logOpts).Stream(ctx)
	if err != nil {
		return err
	}
	defer r.Close()
	return copyPrefixedLines(r, out, mu, prefix)
}

func streamDockerLogs(ctx context.Context, user string, rec hosts.Record, opts LogOptions, out io.Writer, mu *sync.Mutex, prefix string) error {
	containerID, err := dockerprovider.ContainerIDFromRecord(rec.Meta["container_id"])
	if err != nil {
		return err
	}
	dc, err := dockerExecutor{}.Dial(user, rec)
	if err != nil {
		return err
	}
	defer dc.Close()
	native, ok := dc.(*dockerNativeClient)
	if !ok {
		return fmt.Errorf("unexpected docker client type %T", dc)
	}

	logs, err := native.cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Since:      dockerSince(opts.Since),
		Timestamps: opts.Timestamps,
		Follow:     opts.Follow,
		Tail:       strconv.FormatInt(opts.Tail, 10),
	})
	if err != nil {
		return err
	}
	defer logs.Close()

	stdout := newPrefixedWriter(out, mu, prefix)
	stderr := newPrefixedWriter(out, mu, prefix)
	inspect, inspectErr := native.cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if inspectErr == nil && inspect.Container.Config != nil && inspect.Container.Config.Tty {
		_, err = io.Copy(stdout, logs)
	} else {
		_, err = stdcopy.StdCopy(stdout, stderr, logs)
	}
	stdout.Flush()
	stderr.Flush()
	return err
}

func streamExecutorLogs(ctx context.Context, user string, rec hosts.Record, opts LogOptions, cache *ClientCache, out io.Writer, mu *sync.Mutex, prefix string) error {
	cmd, err := logCommandWithRunAs(opts)
	if err != nil {
		return err
	}
	client, err := cache.GetOrDial(user, rec)
	if err != nil {
		return err
	}
	writer := newPrefixedWriter(out, mu, prefix)
	defer writer.Flush()
	done := make(chan error, 1)
	go func() {
		done <- client.RunWithStreams(cmd, nil, writer, writer)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func logCommand(opts LogOptions) string {
	if strings.TrimSpace(opts.Command) != "" {
		return opts.Command
	}
	if looksLikeLogFileSource(opts.Source) {
		flag := "-f"
		if opts.Follow {
			flag = "-F"
		}
		if !opts.Follow {
			return fmt.Sprintf("tail -n %d -- %s", opts.Tail, remoteLogSourceArg(opts.Source))
		}
		return fmt.Sprintf("tail -n %d %s -- %s", opts.Tail, flag, remoteLogSourceArg(opts.Source))
	}
	unit := strings.TrimSpace(opts.Unit)
	if unit == "" {
		unit = strings.TrimSpace(opts.Source)
	}
	if unit == "" {
		unit = strings.TrimSpace(opts.Target)
	}
	args := []string{"journalctl", "-u", shellSingleQuoted(unit), "-n", strconv.FormatInt(opts.Tail, 10), "--no-pager"}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Since > 0 {
		since := time.Now().Add(-opts.Since).Format("2006-01-02 15:04:05")
		args = append(args, "--since", shellSingleQuoted(since))
	}
	if !opts.Timestamps {
		args = append(args, "-o", "cat")
	}
	return strings.Join(args, " ")
}

func logCommandWithRunAs(opts LogOptions) (string, error) {
	return cuetry.WrapRemoteShell(strings.TrimSpace(opts.RunAs), logCommand(opts))
}

func looksLikeLogFileSource(source string) bool {
	source = strings.TrimSpace(source)
	return strings.HasPrefix(source, "/") || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "~/") || strings.ContainsAny(source, "*?[")
}

func remoteLogSourceArg(source string) string {
	if strings.ContainsAny(source, "*?[") {
		return source
	}
	return shellSingleQuoted(source)
}

func dockerSince(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return strconv.FormatInt(time.Now().Add(-d).Unix(), 10)
}

func logPrefix(rec hosts.Record) string {
	backend := strings.TrimSpace(rec.Meta["backend_name"])
	if backend == "" {
		backend = rec.Provider
	}
	return fmt.Sprintf("[%s/%s/%s] ", rec.Provider, backend, rec.Name)
}

func copyPrefixedLines(r io.Reader, out io.Writer, mu *sync.Mutex, prefix string) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		writePrefixedLine(out, mu, prefix, s.Text())
	}
	return s.Err()
}

type prefixedWriter struct {
	out    io.Writer
	mu     *sync.Mutex
	prefix string
	buf    strings.Builder
}

func newPrefixedWriter(out io.Writer, mu *sync.Mutex, prefix string) *prefixedWriter {
	return &prefixedWriter{out: out, mu: mu, prefix: prefix}
}

func (w *prefixedWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			writePrefixedLine(w.out, w.mu, w.prefix, w.buf.String())
			w.buf.Reset()
			continue
		}
		w.buf.WriteByte(b)
	}
	return len(p), nil
}

func (w *prefixedWriter) Flush() {
	if w.buf.Len() == 0 {
		return
	}
	writePrefixedLine(w.out, w.mu, w.prefix, w.buf.String())
	w.buf.Reset()
}

func writePrefixedLine(out io.Writer, mu *sync.Mutex, prefix string, line string) {
	mu.Lock()
	defer mu.Unlock()
	_, _ = fmt.Fprintln(out, prefix+line)
}
