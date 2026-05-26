package cli

import (
	"context"
	"fmt"
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

	return ui.StreamLogs(ctx, sshUser, records, ui.LogOptions{
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
	}, clientCache, os.Stdout)
}
