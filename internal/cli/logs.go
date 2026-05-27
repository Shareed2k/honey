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

	"github.com/shareed2k/honey/internal/ui"
)

var (
	flagLogsFollow         bool
	flagLogsTail           int64
	flagLogsSince          time.Duration
	flagLogsTimestamps     bool
	flagLogsContainer      string
	flagLogsUnit           string
	flagLogsFile           string
	flagLogsCommand        string
	flagLogsRunAs          string
	flagLogsMaxConcurrency int
	flagLogsGrep           string
	flagLogsLabels         []string
	flagLogsTUI            bool
	flagLogsOutputFile     string
	flagLogsHighlight      bool
)

var logsCmd = &cobra.Command{
	Use:   "logs <target> [source]",
	Short: "Aggregate logs across matching hosts, pods, and containers",
	Args:  cobra.RangeArgs(1, 2),
	RunE:  runLogs,
}

func init() {
	rootCmd.AddCommand(logsCmd)
	addSearchCoreFlags(logsCmd, "pods")
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

	opts := ui.LogOptions{
		Target:         target,
		Source:         source,
		Follow:         flagLogsFollow,
		Tail:           flagLogsTail,
		Since:          flagLogsSince,
		Timestamps:     flagLogsTimestamps,
		Container:      flagLogsContainer,
		Unit:           flagLogsUnit,
		Command:        flagLogsCommand,
		RunAs:          flagLogsRunAs,
		MaxConcurrency: flagLogsMaxConcurrency,
		Grep:           flagLogsGrep,
		Labels:         flagLogsLabels,
		Highlight:      flagLogsHighlight,
	}

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
