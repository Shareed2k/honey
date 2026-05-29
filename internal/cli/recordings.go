package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/shareed2k/honey/internal/aichat"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/jsonutil"
	"github.com/shareed2k/honey/internal/recordings"
	"github.com/shareed2k/honey/internal/ui"
	"github.com/spf13/cobra"
)

// resolveRecordingsDir returns the session recordings directory, honouring
// (in order) --record-dir flag, defaults.record_dir in config, and the
// conventional <config-dir>/records fallback. It reuses the config already
// loaded by PersistentPreRunE to avoid a second ResolvePath call.
func resolveRecordingsDir(cmd *cobra.Command) string {
	return config.ResolveRecordDir(resolvedCfg, resolvedCfgPath, flagRecordDir, recordDirFlagChanged(cmd))
}

var recordingsCmd = &cobra.Command{
	Use:   "recordings",
	Short: "Manage session recordings",
}

var recordingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available recordings",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir := resolveRecordingsDir(cmd)
		names, err := recordings.ListHrecBasenames(dir)
		if err != nil {
			return err
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return nil
	},
}

var flagExportOutput string

var recordingsExportCmd = &cobra.Command{
	Use:   "export <basename>",
	Short: "Export a recording to asciinema v3 .cast format",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := resolveRecordingsDir(cmd)
		events, err := recordings.LoadEvents(dir, args[0])
		if err != nil {
			return err
		}
		w := os.Stdout
		if flagExportOutput != "" {
			f, err := os.Create(flagExportOutput) // #nosec G304
			if err != nil {
				return err
			}
			defer f.Close()
			w = f
		}
		return recordings.ExportCast(events, args[0], w)
	},
}

var (
	flagPruneOlderThan string
	flagPruneDryRun    bool
)

var recordingsPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Delete recordings older than a given age",
	Example: `  honey recordings prune --older-than 7d
  honey recordings prune --older-than 720h --dry-run`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		dir := resolveRecordingsDir(cmd)
		maxAge, err := parseRecordingDuration(flagPruneOlderThan)
		if err != nil {
			return fmt.Errorf("--older-than: %w", err)
		}

		if flagPruneDryRun {
			names, err := recordings.ListHrecBasenames(dir)
			if err != nil {
				return err
			}
			cutoff := time.Now().Add(-maxAge)
			count := 0
			for _, name := range names {
				info, statErr := os.Stat(filepath.Join(dir, name))
				if statErr != nil || info.ModTime().After(cutoff) {
					continue
				}
				fmt.Println(name)
				count++
			}
			fmt.Fprintf(os.Stderr, "%d recording(s) would be deleted\n", count)
			return nil
		}

		res, err := recordings.PurgeExpired(dir, maxAge)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "deleted %d recording(s), %d error(s)\n", res.Deleted, res.Errors)
		return nil
	},
}

// parseRecordingDuration extends time.ParseDuration with a "d" (days) suffix.
func parseRecordingDuration(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

var flagStatsJSON bool

var recordingsStatsCmd = &cobra.Command{
	Use:   "stats [basename...]",
	Short: "Show per-recording statistics (duration, bytes, exit code)",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := resolveRecordingsDir(cmd)
		basenames := args
		if len(basenames) == 0 {
			var err error
			basenames, err = recordings.ListHrecBasenames(dir)
			if err != nil {
				return err
			}
		}

		var stats []recordings.SessionStats
		for _, bn := range basenames {
			events, err := recordings.LoadEvents(dir, bn)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", bn, err)
				continue
			}
			stats = append(stats, recordings.ComputeStats(events, bn))
		}

		if flagStatsJSON {
			out, err := jsonutil.MarshalIndent(stats, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", out)
			return err
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "RECORDING\tDURATION\tIN\tOUT\tERRORS\tEXIT")
		for _, s := range stats {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\n",
				s.Basename, s.Duration,
				recordings.FormatBytes(s.StdinBytes),
				recordings.FormatBytes(s.StdoutBytes),
				s.ErrorCount, s.ExitCode,
			)
		}
		return w.Flush()
	},
}

var (
	flagGrepRegex  bool
	flagGrepStdin  bool
	flagGrepBefore int
	flagGrepAfter  int
)

var recordingsGrepCmd = &cobra.Command{
	Use:   "grep <pattern> [basename...]",
	Short: "Search decoded session output across recordings",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern, fileArgs := args[0], args[1:]
		dir := resolveRecordingsDir(cmd)

		basenames := fileArgs
		if len(basenames) == 0 {
			var err error
			basenames, err = recordings.ListHrecBasenames(dir)
			if err != nil {
				return err
			}
		}

		var (
			re  *regexp.Regexp
			err error
		)
		if flagGrepRegex {
			re, err = regexp.Compile(pattern)
		} else {
			re, err = regexp.Compile(`(?i)` + regexp.QuoteMeta(pattern))
		}
		if err != nil {
			return fmt.Errorf("pattern: %w", err)
		}

		multiFile := len(basenames) > 1
		out := cmd.OutOrStdout()
		for _, bn := range basenames {
			events, loadErr := recordings.LoadEvents(dir, bn)
			if loadErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s: %v\n", bn, loadErr)
				continue
			}
			for _, m := range recordings.GrepRecording(events, bn, re, flagGrepStdin, flagGrepBefore, flagGrepAfter) {
				prefix := ""
				if multiFile {
					prefix = m.Basename + ":"
				}
				for _, e := range m.Before {
					fmt.Fprintf(out, "%s%s:%s: %s", prefix, recordings.FormatOffsetMS(e.TimeMS-m.BaseMS), e.Direction, recordings.DecodeDataB64(e.DataB64))
				}
				fmt.Fprintf(out, "%s%s:%s: %s", prefix, recordings.FormatOffsetMS(m.OffsetMS), m.Direction, m.Text)
				for _, e := range m.After {
					fmt.Fprintf(out, "%s%s:%s: %s", prefix, recordings.FormatOffsetMS(e.TimeMS-m.BaseMS), e.Direction, recordings.DecodeDataB64(e.DataB64))
				}
			}
		}
		return nil
	},
}

var flagSummarizeModel string

var recordingsSummarizeCmd = &cobra.Command{
	Use:   "summarize <basename>",
	Short: "Summarize a recording using an LLM (requires OPENAI_API_KEY)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		dir := resolveRecordingsDir(cmd)
		events, err := recordings.LoadEvents(dir, args[0])
		if err != nil {
			return err
		}
		prompt := recordings.BuildSummarizePrompt(events)
		const systemPrompt = `You summarize Honey session recordings of CUE recipe batch runs (.hrec.jsonl).
Focus on: which recipe ran, how many hosts, per-host success/failure, exit codes, and notable stdout/stderr.
Call out failures clearly. Do not invent hosts or steps not present in the log.
Keep the answer structured (short sections or bullets). Do not include secrets or env values.`
		reply, err := aichat.Complete(context.Background(), systemPrompt, prompt, flagSummarizeModel, 1024)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), reply)
		return nil
	},
}

var recordingsReplayCmd = &cobra.Command{
	Use:   "replay <basename>",
	Short: "Replay a session recording in the terminal",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return ui.RunRecordingReplay(resolveRecordingsDir(cmd), args[0])
	},
}

func init() {
	rootCmd.AddCommand(recordingsCmd)
	recordingsCmd.AddCommand(recordingsListCmd, recordingsExportCmd, recordingsPruneCmd, recordingsStatsCmd, recordingsGrepCmd, recordingsSummarizeCmd, recordingsReplayCmd)
	recordingsExportCmd.Flags().StringVarP(&flagExportOutput, "output", "o", "", "Write to file instead of stdout")
	recordingsPruneCmd.Flags().StringVar(&flagPruneOlderThan, "older-than", "", "Delete recordings older than this age (e.g. 7d, 720h)")
	recordingsPruneCmd.Flags().BoolVar(&flagPruneDryRun, "dry-run", false, "List recordings that would be deleted without deleting them")
	_ = recordingsPruneCmd.MarkFlagRequired("older-than")
	recordingsStatsCmd.Flags().BoolVar(&flagStatsJSON, "json", false, "Output as JSON array")
	recordingsGrepCmd.Flags().BoolVarP(&flagGrepRegex, "regex", "E", false, "Treat pattern as a regular expression (default: fixed-string, case-insensitive)")
	recordingsGrepCmd.Flags().BoolVar(&flagGrepStdin, "stdin", false, "Also search stdin events (default: stdout+stderr only)")
	recordingsGrepCmd.Flags().IntVarP(&flagGrepBefore, "before", "B", 0, "Show N events before each match")
	recordingsGrepCmd.Flags().IntVarP(&flagGrepAfter, "after", "A", 0, "Show N events after each match")
	recordingsSummarizeCmd.Flags().StringVar(&flagSummarizeModel, "model", "", "LLM model (default: OPENAI_MODEL env or gpt-4o-mini)")
}
