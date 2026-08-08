package cli

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/safepath"
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Inspect the audit log",
}

// --- audit tail ---

var (
	auditTailRaw    bool
	auditTailSince  string
	auditTailActor  string
	auditTailAction string
	auditTailDec    string
)

var auditTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Stream audit events in real time (like tail -f)",
	RunE:  runAuditTail,
}

func runAuditTail(cmd *cobra.Command, _ []string) error {
	path := auditEffectivePath()
	since, err := parseSince(auditTailSince)
	if err != nil {
		return err
	}
	if !auditLogPresent(path) {
		printAuditLogMissing(cmd.ErrOrStderr(), path)
		return nil
	}

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	defer root.Close()
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	defer f.Close()

	// Seek to end before tailing.
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	w := cmd.OutOrStdout()
	buf := bufio.NewReader(f)
	for {
		line, err := buf.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				return err
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		line = strings.TrimRight(line, "\n")
		if line == "" {
			continue
		}
		var e audit.Event
		if jsonErr := json.Unmarshal([]byte(line), &e); jsonErr != nil {
			continue
		}
		if !matchesFilter(e, since, auditTailActor, auditTailAction, auditTailDec) {
			continue
		}
		if auditTailRaw {
			fmt.Fprintln(w, line)
		} else {
			fmt.Fprintln(w, formatEventLine(e))
		}
	}
}

// --- audit export ---

var (
	auditExportFormat string
	auditExportSince  string
	auditExportActor  string
	auditExportAction string
	auditExportDec    string
)

var auditExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Print audit events as jsonl, table, or csv",
	RunE:  runAuditExport,
}

func runAuditExport(cmd *cobra.Command, _ []string) error {
	path := auditEffectivePath()
	since, err := parseSince(auditExportSince)
	if err != nil {
		return err
	}
	if !auditLogPresent(path) {
		printAuditLogMissing(cmd.ErrOrStderr(), path)
		return nil
	}
	events, err := readAuditEvents(path, since, auditExportActor, auditExportAction, auditExportDec)
	if err != nil {
		return err
	}
	return writeAuditExport(cmd.OutOrStdout(), events, auditExportFormat)
}

// readAuditEvents reads all matching events from path using safepath.ReadFile.
func readAuditEvents(path string, since time.Time, actor, action, decision string) ([]audit.Event, error) {
	data, err := safepath.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}

	var events []audit.Event
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var e audit.Event
		if jsonErr := json.Unmarshal([]byte(line), &e); jsonErr != nil {
			continue
		}
		if matchesFilter(e, since, actor, action, decision) {
			events = append(events, e)
		}
	}
	return events, scanner.Err()
}

// writeAuditExport renders events to w in the chosen format.
func writeAuditExport(w io.Writer, events []audit.Event, format string) error {
	switch format {
	case "jsonl", "":
		for _, e := range events {
			b, _ := json.Marshal(e)
			fmt.Fprintln(w, string(b))
		}
	case "table":
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "TIME\tACTOR\tSOURCE\tACTION\tTARGET\tDECISION\tRISK")
		for _, e := range events {
			fmt.Fprintf(
				tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				e.Time.Format(time.RFC3339),
				e.Actor, e.Source, e.Action, e.Target, e.Decision, e.Risk,
			)
		}
		tw.Flush()
	case "csv":
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"time", "actor", "source", "action", "target", "decision", "risk", "command"})
		for _, e := range events {
			_ = cw.Write([]string{
				e.Time.Format(time.RFC3339),
				e.Actor, e.Source, e.Action, e.Target, e.Decision, e.Risk, e.Command,
			})
		}
		cw.Flush()
	default:
		return fmt.Errorf("unknown format %q (want jsonl, table, csv)", format)
	}
	return nil
}

// --- helpers ---

func auditEffectivePath() string {
	var cfg config.Audit
	if resolvedCfg != nil {
		cfg = resolvedCfg.Audit
	}
	return cfg.EffectivePath()
}

// auditLogPresent reports whether the audit log file exists. When audit is
// enabled the FileSink creates it at startup, so absence means audit is
// disabled (or has simply never run) rather than an error condition.
func auditLogPresent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// printAuditLogMissing explains an absent audit log instead of erroring: it is
// opt-in, so the common case is that it was never enabled.
func printAuditLogMissing(w io.Writer, path string) {
	fmt.Fprintf(w, "no audit log at %s\n", path)
	fmt.Fprintln(w, "audit is opt-in — enable it to record events:")
	fmt.Fprintln(w, "  audit:\n    enabled: true   # in your honey config")
}

func parseSince(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("--since: %w", err)
	}
	return time.Now().Add(-d), nil
}

func matchesFilter(e audit.Event, since time.Time, actor, action, decision string) bool {
	if !since.IsZero() && e.Time.Before(since) {
		return false
	}
	if actor != "" && !strings.EqualFold(e.Actor, actor) {
		return false
	}
	if action != "" && !strings.EqualFold(e.Action, action) {
		return false
	}
	if decision != "" && !strings.EqualFold(e.Decision, decision) {
		return false
	}
	return true
}

func formatEventLine(e audit.Event) string {
	dec := e.Decision
	if dec == "" {
		dec = "-"
	}
	risk := e.Risk
	if risk == "" {
		risk = "-"
	}
	ts := e.Time.Format("15:04:05")
	return fmt.Sprintf("[%s] actor=%-12s action=%-14s target=%-20s decision=%s risk=%s",
		ts, e.Actor, e.Action, e.Target, dec, risk)
}

func init() {
	auditTailCmd.Flags().BoolVar(&auditTailRaw, "raw", false, "Print raw JSON lines")
	auditTailCmd.Flags().StringVar(&auditTailSince, "since", "", "Only show events after this duration ago (e.g. 1h, 30m)")
	auditTailCmd.Flags().StringVar(&auditTailActor, "actor", "", "Filter by actor")
	auditTailCmd.Flags().StringVar(&auditTailAction, "action", "", "Filter by action (exec, recipe_run, approval)")
	auditTailCmd.Flags().StringVar(&auditTailDec, "decision", "", "Filter by decision (allow, deny, require_approval)")

	auditExportCmd.Flags().StringVar(&auditExportFormat, "format", "jsonl", "Output format: jsonl, table, csv")
	auditExportCmd.Flags().StringVar(&auditExportSince, "since", "", "Only show events after this duration ago (e.g. 1h, 30m)")
	auditExportCmd.Flags().StringVar(&auditExportActor, "actor", "", "Filter by actor")
	auditExportCmd.Flags().StringVar(&auditExportAction, "action", "", "Filter by action")
	auditExportCmd.Flags().StringVar(&auditExportDec, "decision", "", "Filter by decision")

	auditCmd.AddCommand(auditTailCmd, auditExportCmd)
	rootCmd.AddCommand(auditCmd)
}
