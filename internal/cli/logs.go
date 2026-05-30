package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/anomaly"
	"github.com/shareed2k/honey/internal/ui"
)

var (
	flagLogsFollow                 bool
	flagLogsTail                   int64
	flagLogsSince                  time.Duration
	flagLogsTimestamps             bool
	flagLogsContainer              string
	flagLogsUnit                   string
	flagLogsFile                   string
	flagLogsCommand                string
	flagLogsRunAs                  string
	flagLogsMaxConcurrency         int
	flagLogsGrep                   string
	flagLogsLabels                 []string
	flagLogsTUI                    bool
	flagLogsOutputFile             string
	flagLogsHighlight              bool
	flagLogsAnomaly                bool
	flagLogsAnomalyModel           string
	flagLogsAnomalyThresh          float64
	flagLogsAnomalyWindow          int
	flagLogsAnomalyOnly            bool
	flagLogsAnomalyStrict          bool
	flagLogsAnomalyTokPath         string
	flagLogsAnomalySelftest        bool
	flagLogsAnomalyEndpoint        string
	flagLogsAnomalyLLMModel        string
	flagLogsAnomalyContextLines    int
	flagLogsAnomalyFilterThreshold float64
	flagLogsAnomalyFreqWindow      int
	flagLogsAnomalyFreqRatio       float64
	flagLogsAnomalyFeedbackFile    string
	flagLogsAlert                  bool
	flagLogsAlertSuppress          time.Duration
)

var logsCmd = &cobra.Command{
	Use:   "logs <target> [source]",
	Short: "Aggregate logs across matching hosts, pods, and containers",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
	addSearchCoreFlags(logsCmd)
	logsCmd.Flags().BoolVarP(&flagLogsFollow, "follow", "f", false, "Follow logs")
	logsCmd.Flags().Int64Var(&flagLogsTail, "tail", 100, "Number of lines to show from the end")
	logsCmd.Flags().DurationVar(&flagLogsSince, "since", 0, "Only show logs newer than duration ago (e.g. 10m, 1h)")
	logsCmd.Flags().BoolVar(&flagLogsTimestamps, "timestamps", false, "Include provider timestamps when supported")
	logsCmd.Flags().StringVar(&flagLogsContainer, "container", "", "Kubernetes container name for multi-container pods")
	logsCmd.Flags().StringVar(&flagLogsUnit, "unit", "", "Systemd unit for SSH-like records")
	logsCmd.Flags().StringVar(&flagLogsFile, "file", "", "Remote log file or glob to tail")
	logsCmd.Flags().StringVar(&flagLogsCommand, "cmd", "", "Custom remote log command for executor-backed records")
	logsCmd.Flags().StringVar(&flagLogsRunAs, "run-as", "", "Run executor-backed log command as this remote user via sudo -n")
	logsCmd.Flags().IntVar(&flagLogsMaxConcurrency, "max-concurrency", 8, "Maximum concurrent log streams")
	logsCmd.Flags().StringVarP(&flagLogsGrep, "grep", "g", "", "Filter logs by case-insensitive regex or substring")
	logsCmd.Flags().StringSliceVarP(&flagLogsLabels, "label", "l", nil, "Additional host labels to show in prefix (comma-separated)")
	logsCmd.Flags().BoolVar(&flagLogsTUI, "tui", false, "Use interactive log viewer")
	logsCmd.Flags().StringVarP(&flagLogsOutputFile, "output-file", "o", "", "Write combined log stream to this local file")
	logsCmd.Flags().BoolVar(&flagLogsHighlight, "highlight", true, "Highlight error-like keywords in logs")
	logsCmd.Flags().BoolVar(&flagLogsAnomaly, "anomaly", false, "Enable embedded anomaly detection for log lines")
	logsCmd.Flags().StringVar(&flagLogsAnomalyModel, "anomaly-model", "", "Path to local ONNX model file (used by embedded detector)")
	logsCmd.Flags().Float64Var(&flagLogsAnomalyThresh, "anomaly-threshold", 0.90, "Anomaly score threshold between 0 and 1")
	logsCmd.Flags().IntVar(&flagLogsAnomalyWindow, "anomaly-window", 32, "Sliding window size for anomaly scoring")
	logsCmd.Flags().BoolVar(&flagLogsAnomalyOnly, "anomaly-only", false, "Only show lines that exceed anomaly threshold")
	logsCmd.Flags().BoolVar(&flagLogsAnomalyStrict, "anomaly-strict", false, "Fail startup if anomaly detector cannot initialize")
	logsCmd.Flags().StringVar(&flagLogsAnomalyTokPath, "anomaly-tokenizer", "", "Path to DistilBERT vocab.txt tokenizer file")
	logsCmd.Flags().BoolVar(&flagLogsAnomalySelftest, "anomaly-selftest", false, "Validate anomaly model/tokenizer/runtime and run a local score smoke test")
	logsCmd.Flags().StringVar(&flagLogsAnomalyEndpoint, "anomaly-endpoint", "", "OpenAI-compatible API base URL for LLM anomaly scoring (Ollama: http://localhost:11434/v1, LM Studio: http://localhost:1234/v1)")
	logsCmd.Flags().StringVar(&flagLogsAnomalyLLMModel, "anomaly-llm-model", "llama3", "Model name for --anomaly-endpoint. Smaller models (3-7B) typically match or beat larger ones for binary log anomaly classification")
	logsCmd.Flags().IntVar(&flagLogsAnomalyContextLines, "anomaly-context", 5, "Number of recent lines sent as context to the LLM (0 = single-line mode)")
	logsCmd.Flags().Float64Var(&flagLogsAnomalyFilterThreshold, "anomaly-filter-threshold", 0, "Skip LLM when fast detector score is below this value (0=disabled, 0.40=recommended for CoLA-style two-tier detection)")
	logsCmd.Flags().IntVar(&flagLogsAnomalyFreqWindow, "anomaly-freq-window", 100, "Short window size for rate-ratio burst detection (0=disabled)")
	logsCmd.Flags().Float64Var(&flagLogsAnomalyFreqRatio, "anomaly-freq-ratio", 5.0, "Short/long rate ratio above which a log template is flagged as a frequency spike")
	logsCmd.Flags().StringVar(&flagLogsAnomalyFeedbackFile, "anomaly-feedback-file", "", "Append scored log lines as JSONL to this file for review and threshold calibration")
	logsCmd.Flags().BoolVar(&flagLogsAlert, "alert", false, "Send anomaly notifications via HONEY_NOTIFY_* env vars (auto-enables --anomaly)")
	logsCmd.Flags().DurationVar(&flagLogsAlertSuppress, "alert-suppress", 5*time.Minute, "Suppress repeated alerts for the same source+reason pair for this duration (0=no dedup)")
}

func runLogs(cmd *cobra.Command, args []string) error {
	target := strings.TrimSpace(args[0])
	source := ""
	if len(args) == 2 {
		source = strings.TrimSpace(args[1])
	}
	if flagLogsFile != "" {
		source = flagLogsFile
	}

	if flagLogsAnomalyEndpoint != "" {
		flagLogsAnomaly = true
	}
	if flagLogsAlert {
		flagLogsAnomaly = true
	}
	if flagLogsAnomalyEndpoint != "" && flagLogsAnomalyModel != "" {
		fmt.Fprintln(os.Stderr, "warning: ensemble mode active (ONNX + LLM) — both detectors score every log line in parallel; throughput is limited by LLM response time (~1–5 s/line)")
	}

	opts := ui.LogOptions{
		Target:                 target,
		Source:                 source,
		Follow:                 flagLogsFollow,
		Tail:                   flagLogsTail,
		Since:                  flagLogsSince,
		Timestamps:             flagLogsTimestamps,
		Container:              flagLogsContainer,
		Unit:                   flagLogsUnit,
		Command:                flagLogsCommand,
		RunAs:                  flagLogsRunAs,
		MaxConcurrency:         flagLogsMaxConcurrency,
		Grep:                   flagLogsGrep,
		Labels:                 flagLogsLabels,
		Highlight:              flagLogsHighlight,
		Anomaly:                flagLogsAnomaly,
		AnomalyModel:           flagLogsAnomalyModel,
		AnomalyThresh:          flagLogsAnomalyThresh,
		AnomalyWindow:          flagLogsAnomalyWindow,
		AnomalyOnly:            flagLogsAnomalyOnly,
		AnomalyStrict:          flagLogsAnomalyStrict,
		AnomalyTokPath:         flagLogsAnomalyTokPath,
		AnomalyEndpoint:        flagLogsAnomalyEndpoint,
		AnomalyLLMModel:        flagLogsAnomalyLLMModel,
		AnomalyContextLines:    flagLogsAnomalyContextLines,
		AnomalyFilterThreshold: flagLogsAnomalyFilterThreshold,
		AnomalyFreqWindow:      flagLogsAnomalyFreqWindow,
		AnomalyFreqRatio:       flagLogsAnomalyFreqRatio,
		AnomalyFeedbackFile:    flagLogsAnomalyFeedbackFile,
		AlertEnabled:           flagLogsAlert,
		AlertSuppressDuration:  flagLogsAlertSuppress,
	}

	if flagLogsAnomalySelftest {
		return runAnomalySelftest(opts)
	}

	clientCache := ui.NewClientCache()
	ui.SetDockerSSHBorrowCache(clientCache)
	defer clientCache.CloseAll()

	records, sshUser, _, _, err := runSearchCore(cmd, []string{target})
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("no records match %q", target)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if flagLogsTUI {
		return ui.RunLogTUI(ctx, sshUser, records, opts, clientCache)
	}

	var out io.Writer = os.Stdout
	if flagLogsOutputFile != "" {
		f, err := os.Create(flagLogsOutputFile) // #nosec G304 -- destination controlled by user flag
		if err != nil {
			return fmt.Errorf("output file: %w", err)
		}
		defer f.Close()
		out = io.MultiWriter(os.Stdout, f)
	}

	return ui.StreamLogs(ctx, sshUser, records, opts, clientCache, out)
}

func runAnomalySelftest(opts ui.LogOptions) error {
	if !opts.Anomaly {
		return fmt.Errorf("--anomaly-selftest requires --anomaly")
	}
	det, err := anomaly.NewEmbeddedDetector(anomaly.Options{
		ModelPath:     strings.TrimSpace(opts.AnomalyModel),
		TokenizerPath: strings.TrimSpace(opts.AnomalyTokPath),
		Threshold:     opts.AnomalyThresh,
		Window:        opts.AnomalyWindow,
	})
	if err != nil {
		return fmt.Errorf("anomaly selftest init: %w", err)
	}
	samples := []string{
		"INFO startup complete",
		"ERROR authentication failed for user root",
	}
	fmt.Fprintln(os.Stdout, "anomaly selftest ok: detector initialized")
	for _, sample := range samples {
		res, scoreErr := det.Score(context.Background(), sample)
		if scoreErr != nil {
			return fmt.Errorf("anomaly selftest score: %w", scoreErr)
		}
		fmt.Fprintf(os.Stdout, "sample=%q score=%.4f anomaly=%t reason=%s\n", sample, res.Score, res.Anomaly, res.Reason)
	}
	return nil
}
