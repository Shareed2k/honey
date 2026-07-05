package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/safepath"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/shareed2k/honey/internal/alerts"
	"github.com/shareed2k/honey/internal/anomaly"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/jsonutil"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"github.com/shareed2k/honey/internal/recipenotify"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

type feedbackWriter struct {
	mu       *sync.Mutex
	w        io.Writer
	rawCache *lru.Cache[string, bool] // Tier 1: Raw line bypass cache
	written  *lru.Cache[string, bool] // Tier 2: Normalized uniqueness filter
}

type feedbackRecord struct {
	Ts      string  `json:"ts"`
	Source  string  `json:"source"`
	Line    string  `json:"line"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
	Anomaly bool    `json:"anomaly"`
}

func (f *feedbackWriter) write(source, line string, r anomaly.Result) {
	// Tier 1 Fast Path: Check if we have already written this exact raw line recently.
	// This takes < 50ns and performs zero string manipulations or regex runs!
	if f.rawCache != nil {
		ok, _ := f.rawCache.ContainsOrAdd(line, true)
		if ok {
			return // Already written! Bypassed regexes completely.
		}
	}

	// Tier 2: New raw line pattern. We run Normalize only once.
	norm := anomaly.Normalize(line)
	if f.written != nil {
		ok, _ := f.written.ContainsOrAdd(norm, true)
		if ok {
			return
		}
	}

	rec := feedbackRecord{
		Ts:      time.Now().UTC().Format(time.RFC3339),
		Source:  source,
		Line:    line,
		Score:   r.Score,
		Reason:  r.Reason,
		Anomaly: r.Anomaly,
	}
	b, err := jsonutil.Marshal(rec)
	if err != nil {
		return
	}
	f.mu.Lock()
	_, _ = f.w.Write(append(b, '\n'))
	f.mu.Unlock()
}

// LogOptions controls distributed log streaming.
type LogOptions struct {
	Target                 string
	Source                 string
	Follow                 bool
	Tail                   int64
	Since                  time.Duration
	Timestamps             bool
	Container              string
	Unit                   string
	Command                string
	RunAs                  string
	MaxConcurrency         int
	Grep                   string
	Labels                 []string
	Highlight              bool
	Anomaly                bool
	AnomalyModel           string
	AnomalyThresh          float64
	AnomalyWindow          int
	AnomalyOnly            bool
	AnomalyStrict          bool
	AnomalyTokPath         string
	AnomalyEndpoint        string
	AnomalyLLMModel        string
	AnomalyContextLines    int
	AnomalyFilterThreshold float64
	AnomalyFreqWindow      int
	AnomalyFreqRatio       float64
	AnomalyFeedbackFile    string
	AnomalyPreprocessor    string
	AlertEnabled           bool
	AlertSuppressDuration  time.Duration
}

// StreamLogs streams logs for records to out with stable per-record prefixes.
func StreamLogs(ctx context.Context, user string, records []hosts.Record, opts LogOptions, cache *engine.ClientCache, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	if opts.Tail <= 0 {
		opts.Tail = 100
	}
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = 8
	}

	var grepRe *regexp.Regexp
	if opts.Grep != "" {
		re, err := regexp.Compile("(?i)" + opts.Grep)
		if err != nil {
			return fmt.Errorf("invalid grep regex: %w", err)
		}
		grepRe = re
	}

	var detector anomaly.Detector
	if opts.Anomaly {
		if err := anomaly.LoadFeedbackDemos(opts.AnomalyFeedbackFile); err != nil {
			if opts.AnomalyStrict {
				return fmt.Errorf("load feedback demos: %w", err)
			}
			zap.L().Warn("failed to load feedback demos", zap.Error(err))
		}

		d, err := anomaly.NewEmbeddedDetector(anomaly.Options{
			ModelPath:       strings.TrimSpace(opts.AnomalyModel),
			Threshold:       opts.AnomalyThresh,
			Window:          opts.AnomalyWindow,
			TokenizerPath:   strings.TrimSpace(opts.AnomalyTokPath),
			LLMEndpoint:     strings.TrimSpace(opts.AnomalyEndpoint),
			LLMModel:        strings.TrimSpace(opts.AnomalyLLMModel),
			LLMContextLines: opts.AnomalyContextLines,
			FilterThreshold: opts.AnomalyFilterThreshold,
			FreqWindow:      opts.AnomalyFreqWindow,
			FreqRatio:       opts.AnomalyFreqRatio,
			Preprocessor:    strings.TrimSpace(opts.AnomalyPreprocessor),
		})
		if err != nil {
			if opts.AnomalyStrict {
				return fmt.Errorf("initialize anomaly detector: %w", err)
			}
			zap.L().Debug("anomaly detector disabled", zap.Error(err))
			_, _ = fmt.Fprintf(os.Stderr, "warning: anomaly detector disabled: %v\n", err)
		} else {
			zap.L().Debug(
				"anomaly detector initialized",
				zap.String("model", opts.AnomalyModel),
				zap.Float64("threshold", opts.AnomalyThresh),
				zap.Int("freqWindow", opts.AnomalyFreqWindow),
				zap.Float64("freqRatio", opts.AnomalyFreqRatio),
			)
			detector = d
		}
	}

	var fbw *feedbackWriter
	if opts.AnomalyFeedbackFile != "" {
		f, err := safepath.OpenFile(opts.AnomalyFeedbackFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("feedback file: %w", err)
		}
		defer f.Close()

		raw, _ := lru.New[string, bool](10_000)
		c, _ := lru.New[string, bool](10_000)
		fbw = &feedbackWriter{
			mu:       &sync.Mutex{},
			w:        f,
			rawCache: raw,
			written:  c,
		}
	}

	var disp *alerts.Dispatcher
	if opts.AlertEnabled && recipenotify.EnvHasAnyReceiver() {
		dur := opts.AlertSuppressDuration
		if dur == 0 {
			dur = 5 * time.Minute
		}
		n, _ := recipenotify.BuildFromEnv()
		disp = alerts.New(n, dur)
		defer disp.Close()
	}

	zap.L().Debug(
		"StreamLogs start",
		zap.Int("records", len(records)),
		zap.Bool("follow", opts.Follow),
		zap.Int64("tail", opts.Tail),
		zap.String("grep", opts.Grep),
		zap.Bool("anomaly", opts.Anomaly),
	)

	g, ctx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, opts.MaxConcurrency)
	var writeMu sync.Mutex
	run := logRun{user: user, opts: opts, cache: cache, reg: cache.Registry()}
	baseSink := logSink{
		out:         out,
		mu:          &writeMu,
		grepRe:      grepRe,
		detector:    detector,
		fbw:         fbw,
		disp:        disp,
		highlight:   opts.Highlight,
		anomalyOnly: opts.AnomalyOnly,
	}
	for _, rec := range records {
		rec := rec
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return ctx.Err()
			}
			return streamOneLog(ctx, run, rec, baseSink)
		})
	}
	return g.Wait()
}

// logRun holds the run-level configuration shared across every per-record log stream.
type logRun struct {
	user  string
	opts  LogOptions
	cache *engine.ClientCache
	reg   hostexec.Registry
}

// logSink holds the per-stream output/rendering configuration (previously passed
// as 7+ positional parameters through the log-streaming helpers).
type logSink struct {
	out         io.Writer
	mu          *sync.Mutex
	prefix      string
	grepRe      *regexp.Regexp
	detector    anomaly.Detector
	fbw         *feedbackWriter
	disp        *alerts.Dispatcher
	highlight   bool
	anomalyOnly bool
}

func streamOneLog(ctx context.Context, run logRun, rec hosts.Record, sink logSink) error {
	zap.L().Debug(
		"streamOneLog",
		zap.String("record", rec.Name),
		zap.String("provider", rec.Provider),
		zap.String("kind", rec.Meta["kind"]),
	)
	sink.prefix = logPrefix(rec, run.opts.Labels)
	if run.opts.Command != "" || run.opts.Unit != "" || strings.TrimSpace(run.opts.Source) != "" {
		return streamExecutorLogs(ctx, run, rec, sink)
	}

	switch {
	case rec.Provider == "k8s" && rec.Meta["kind"] == "pod":
		return streamK8sPodLogs(ctx, run, rec, sink)
	case rec.Provider == "docker" && (rec.Meta["kind"] == "container" || rec.Meta["kind"] == "swarm_task"):
		return streamDockerLogs(ctx, run, rec, sink)
	default:
		return streamExecutorLogs(ctx, run, rec, sink)
	}
}

func streamK8sPodLogs(ctx context.Context, run logRun, rec hosts.Record, sink logSink) error {
	opts := run.opts
	namespace := strings.TrimSpace(rec.Meta["namespace"])
	podName := strings.TrimSpace(rec.Meta["pod_name"])
	if namespace == "" || podName == "" {
		return fmt.Errorf("%s missing k8s namespace or pod_name", rec.Name)
	}
	zap.L().Debug(
		"k8s pod logs",
		zap.String("namespace", namespace),
		zap.String("pod", podName),
		zap.String("container", opts.Container),
	)

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
	return copyPrefixedLines(ctx, sink, r)
}

func streamDockerLogs(ctx context.Context, run logRun, rec hosts.Record, sink logSink) error {
	opts := run.opts
	containerID, err := dockerprovider.ContainerIDFromRecord(rec.Meta["container_id"])
	if err != nil {
		return err
	}
	zap.L().Debug(
		"docker container logs",
		zap.String("record", rec.Name),
		zap.String("containerID", containerID),
	)
	dc, err := run.reg.ForRecord(rec).Dial(run.user, rec)
	if err != nil {
		return err
	}
	defer dc.Close()
	native, ok := dc.(*engine.DockerNativeClient)
	if !ok {
		return fmt.Errorf("unexpected docker client type %T", dc)
	}

	logs, err := native.Cli.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
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

	stdout := newPrefixedWriter(ctx, sink)
	stderr := newPrefixedWriter(ctx, sink)
	inspect, inspectErr := native.Cli.ContainerInspect(ctx, containerID, client.ContainerInspectOptions{})
	if inspectErr == nil && inspect.Container.Config != nil && inspect.Container.Config.Tty {
		_, err = io.Copy(stdout, logs)
	} else {
		_, err = stdcopy.StdCopy(stdout, stderr, logs)
	}
	stdout.Flush()
	stderr.Flush()
	return err
}

func streamExecutorLogs(ctx context.Context, run logRun, rec hosts.Record, sink logSink) error {
	cmd, err := logCommandWithRunAs(run.opts)
	if err != nil {
		return err
	}
	zap.L().Debug(
		"executor logs",
		zap.String("record", rec.Name),
		zap.String("command", cmd),
		zap.String("runAs", run.opts.RunAs),
	)
	client, err := run.cache.GetOrDial(run.user, rec)
	if err != nil {
		return err
	}
	writer := newPrefixedWriter(ctx, sink)
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
	args := []string{"journalctl", "-u", engine.ShellSingleQuoted(unit), "-n", strconv.FormatInt(opts.Tail, 10), "--no-pager"}
	if opts.Follow {
		args = append(args, "-f")
	}
	if opts.Since > 0 {
		since := time.Now().Add(-opts.Since).Format("2006-01-02 15:04:05")
		args = append(args, "--since", engine.ShellSingleQuoted(since))
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
	return engine.ShellSingleQuoted(source)
}

func dockerSince(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return strconv.FormatInt(time.Now().Add(-d).Unix(), 10)
}

func logPrefix(rec hosts.Record, labelKeys []string) string {
	backend := strings.TrimSpace(rec.Meta["backend_name"])
	if backend == "" {
		backend = rec.Provider
	}
	prefix := fmt.Sprintf("[%s/%s/%s", rec.Provider, backend, rec.Name)
	var labels []string
	for _, k := range labelKeys {
		k = strings.TrimSpace(k)
		if v, ok := rec.Meta[k]; ok {
			labels = append(labels, fmt.Sprintf("%s=%s", k, v))
		} else if v, ok := rec.Meta["label_"+k]; ok {
			labels = append(labels, fmt.Sprintf("%s=%s", k, v))
		} else if v, ok := rec.Meta["annotation_"+k]; ok {
			labels = append(labels, fmt.Sprintf("%s=%s", k, v))
		}
	}
	if len(labels) > 0 {
		prefix += " | " + strings.Join(labels, " ")
	}
	prefix += "] "
	return prefix
}

func copyPrefixedLines(ctx context.Context, sink logSink, r io.Reader) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		writePrefixedLine(ctx, sink, s.Text())
	}
	return s.Err()
}

type prefixedWriter struct {
	ctx  context.Context
	sink logSink
	buf  strings.Builder
}

func newPrefixedWriter(ctx context.Context, sink logSink) *prefixedWriter {
	return &prefixedWriter{ctx: ctx, sink: sink}
}

func (w *prefixedWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			writePrefixedLine(w.ctx, w.sink, w.buf.String())
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
	writePrefixedLine(w.ctx, w.sink, w.buf.String())
	w.buf.Reset()
}

func writePrefixedLine(ctx context.Context, sink logSink, line string) {
	if sink.grepRe != nil && !sink.grepRe.MatchString(line) {
		return
	}
	if sink.detector != nil {
		res, err := sink.detector.Score(ctx, line)
		if err != nil {
			if sink.anomalyOnly {
				return
			}
		} else {
			if sink.fbw != nil {
				sink.fbw.write(sink.prefix, line, res)
			}
			if sink.anomalyOnly && !res.Anomaly {
				return
			}
			if res.Anomaly {
				zap.L().Debug(
					"anomaly detected",
					zap.String("prefix", strings.TrimSpace(sink.prefix)),
					zap.Float64("score", res.Score),
					zap.String("reason", res.Reason),
				)
				if sink.disp != nil {
					sink.disp.Dispatch(ctx, strings.TrimSpace(sink.prefix), res.Score, res.Reason, line)
				}
				line = fmt.Sprintf("[ANOM score=%.2f reason=%s] %s", res.Score, res.Reason, line)
			}
		}
	}
	if sink.highlight {
		line = highlightLogLine(line)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	_, _ = fmt.Fprintln(sink.out, sink.prefix+line)
}

var (
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
)

func highlightLogLine(line string) string {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "error") || strings.Contains(lower, "fail") || strings.Contains(lower, "fatal") || strings.Contains(lower, "panic") || strings.Contains(lower, "exception"):
		return errorStyle.Render(line)
	case strings.Contains(lower, "warn"):
		return warnStyle.Render(line)
	case strings.Contains(lower, "info"):
		// Maybe info highlighting is too much? Let's keep it subtle or skip.
		return line
	}
	// Highlight HTTP status codes
	if strings.Contains(line, "HTTP/") {
		if strings.Contains(line, " 50") {
			return errorStyle.Render(line)
		}
		if strings.Contains(line, " 40") {
			return warnStyle.Render(line)
		}
	}
	return line
}
