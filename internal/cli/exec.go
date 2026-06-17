package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/spf13/cobra"
)

const timeoutMissingMarker = "__HONEY_TIMEOUT_MISSING__"

var (
	flagExecParallel int
	flagExecRetry    int
	flagExecTimeout  time.Duration
	flagExecRunAs    string
	flagExecShell    string
	flagExecQuiet    bool
	flagExecOutput   string
)

var execCmd = &cobra.Command{
	Use:   "exec <target> <command>",
	Short: "Run a shell command on matching hosts in parallel",
	Long: `Runs a shell command on all connectable records returned by search.

Supports SSH hosts, Kubernetes pods, Docker containers/tasks, and TrueNAS records
through the same executor pipeline used by the TUI and recipe execution.`,
	Example: `  honey exec "web-*" --parallel 50 --retry 3 --timeout 10s "systemctl restart nginx"
  honey --backends gcp-stg2 exec postgres /usr/bin/uptime
  honey exec "api-*" --provider k8s --run-as root "journalctl -u nginx -n 50"`,
	Args: cobra.MinimumNArgs(2),
	RunE: runExec,
}

func init() {
	rootCmd.AddCommand(execCmd)
	addSearchCoreFlags(execCmd)
	// exec targets pods by default (unlike search which defaults to nodes).
	// Override the default registered by k8sprovider.RegisterFlags without
	// marking the flag as Changed, so an explicit --k8s-mode still takes precedence.
	if f := execCmd.Flags().Lookup("k8s-mode"); f != nil {
		f.DefValue = "pods"
		_ = f.Value.Set("pods")
	}
	execCmd.Flags().IntVar(&flagExecParallel, "parallel", 20, "Maximum concurrent command executions")
	execCmd.Flags().IntVar(&flagExecRetry, "retry", 1, "Retry attempts per host (1 disables retries)")
	execCmd.Flags().DurationVar(&flagExecTimeout, "timeout", 0, "Per-host command timeout (e.g. 10s, 2m); 0 disables")
	execCmd.Flags().StringVar(&flagExecRunAs, "run-as", "", "Run command as this remote user via sudo -n")
	execCmd.Flags().StringVar(&flagExecShell, "shell", "auto", "Command shell: auto, sh, bash, raw, powershell")
	execCmd.Flags().BoolVar(&flagExecQuiet, "quiet", false, "Show status lines only (no stdout blocks)")
	execCmd.Flags().StringVarP(&flagExecOutput, "output", "o", "text", "Output format: text or json")
}

func validateExecFlags() error {
	if flagExecParallel <= 0 {
		return fmt.Errorf("--parallel must be > 0")
	}
	if flagExecRetry <= 0 {
		return fmt.Errorf("--retry must be > 0")
	}
	if flagExecOutput != "text" && flagExecOutput != "json" {
		return fmt.Errorf("--output must be one of: text, json")
	}
	if !isValidExecShell(flagExecShell) {
		return fmt.Errorf("--shell must be one of: auto, sh, bash, raw, powershell")
	}
	if strings.EqualFold(flagExecShell, "powershell") && strings.TrimSpace(flagExecRunAs) != "" {
		return fmt.Errorf("--run-as is not supported with --shell powershell")
	}
	return nil
}

func printExecResult(res engine.HostExecResult) {
	prefix := fmt.Sprintf("[%s/%s/%s]", res.Provider, res.Name, strings.TrimSpace(res.IP))
	output := strings.TrimSpace(res.Output)
	if res.Success {
		fmt.Fprintf(os.Stdout, "%s ok\n", prefix)
		if !flagExecQuiet && output != "" {
			fmt.Fprintf(os.Stdout, "%s stdout:\n%s\n", prefix, output)
		}
		return
	}
	errMsg := strings.TrimSpace(res.ErrMsg)
	if errMsg == "" {
		errMsg = "command failed"
	}
	if res.ExitCode == 124 && strings.Contains(res.Output, timeoutMissingMarker) {
		errMsg = "remote host missing `timeout` command (install coreutils or set --timeout=0)"
	}
	if res.ExitCode != 0 {
		fmt.Fprintf(os.Stdout, "%s fail exit=%d err=%s\n", prefix, res.ExitCode, errMsg)
	} else {
		fmt.Fprintf(os.Stdout, "%s fail err=%s\n", prefix, errMsg)
	}
	if !flagExecQuiet && output != "" {
		fmt.Fprintf(os.Stdout, "%s stdout:\n%s\n", prefix, output)
	}
}

func runExec(cmd *cobra.Command, args []string) error {
	if err := validateExecFlags(); err != nil {
		return err
	}

	target := strings.TrimSpace(args[0])
	remoteCmd := strings.TrimSpace(strings.Join(args[1:], " "))
	if remoteCmd == "" {
		return fmt.Errorf("command is required")
	}

	reg := buildHostExecRegistry()
	cfg := resolvedCfg
	reg.Reconfigure(cfg)
	clientCache := engine.NewClientCache()
	clientCache.SetRegistry(reg)
	defer clientCache.CloseAll()

	records, sshUser, _, _, err := runSearchCore(cmd, []string{target})
	if err != nil {
		return err
	}

	jobs := make([]hosts.Record, 0, len(records))
	for _, r := range records {
		if hosts.IsConnectableRecord(r) {
			jobs = append(jobs, r)
		}
	}
	if len(jobs) == 0 {
		return fmt.Errorf("no connectable records match %q", target)
	}

	finalCmd, err := buildExecCommand(remoteCmd, flagExecTimeout, strings.TrimSpace(flagExecRunAs), strings.TrimSpace(flagExecShell))
	if err != nil {
		return err
	}

	retryCfg := cuetry.RecipeStepRetry{}
	if flagExecRetry > 1 {
		retryCfg = cuetry.RecipeStepRetry{Attempts: flagExecRetry, DelayMS: 1000, MaxDelayMS: 30000, Backoff: "fixed"}
	}

	out := make(chan engine.HostExecResult, len(jobs))
	go func() {
		defer close(out)
		_ = engine.StreamSSHParallel(
			context.Background(), sshUser, jobs, false,
			func(_ hosts.Record, _ map[string]string) string { return finalCmd },
			out, engine.BatchOptions{
				MaxConc:    flagExecParallel,
				Cache:      clientCache,
				RetryCfg:   retryCfg,
				AttemptMax: new(atomic.Int32),
				Reg:        reg,
			},
		)
	}()

	results := make([]engine.HostExecResult, 0, len(jobs))
	total := 0
	failures := 0
	for res := range out {
		results = append(results, res)
		total++
		if !res.Success {
			failures++
		}
		if flagExecOutput != "json" {
			printExecResult(res)
		}
	}

	if flagExecOutput == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
	}

	if failures > 0 {
		return fmt.Errorf("exec completed with failures: %d/%d failed", failures, total)
	}
	if flagExecOutput == "text" {
		fmt.Fprintf(os.Stdout, "exec completed: %d/%d succeeded\n", total-failures, total)
	}
	return nil
}

func buildExecCommand(command string, timeout time.Duration, runAs, shellMode string) (string, error) {
	shellMode = strings.ToLower(strings.TrimSpace(shellMode))
	if shellMode == "" || shellMode == "auto" {
		shellMode = "sh"
	}
	var inner string
	if timeout > 0 {
		switch shellMode {
		case "sh", "bash":
			inner = fmt.Sprintf("command -v timeout >/dev/null 2>&1 || { echo %s >&2; exit 124; }; timeout %s %s -lc %s", shellSingleQuote(timeoutMissingMarker), timeout.String(), shellMode, shellSingleQuote(command))
		case "raw":
			inner = fmt.Sprintf("command -v timeout >/dev/null 2>&1 || { echo %s >&2; exit 124; }; timeout %s %s", shellSingleQuote(timeoutMissingMarker), timeout.String(), command)
		case "powershell":
			inner = powershellTimeoutCommand(command, timeout)
		default:
			return "", fmt.Errorf("unsupported shell mode %q", shellMode)
		}
	} else {
		switch shellMode {
		case "sh", "bash":
			inner = fmt.Sprintf("%s -lc %s", shellMode, shellSingleQuote(command))
		case "raw":
			inner = command
		case "powershell":
			inner = powershellCommand(command)
		default:
			return "", fmt.Errorf("unsupported shell mode %q", shellMode)
		}
	}
	if runAs == "" {
		return inner, nil
	}
	return cuetry.WrapRemoteShell(runAs, inner)
}

func isValidExecShell(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "auto", "sh", "bash", "raw", "powershell":
		return true
	default:
		return false
	}
}

func powershellCommand(command string) string {
	return fmt.Sprintf("powershell -NoProfile -NonInteractive -Command %s", shellSingleQuote(command))
}

func powershellTimeoutCommand(command string, timeout time.Duration) string {
	secs := int(timeout.Seconds())
	if secs <= 0 {
		secs = 1
	}
	ps := fmt.Sprintf("$job = Start-Job -ScriptBlock { %s }; if (Wait-Job $job -Timeout %d) { Receive-Job $job; if ($job.State -eq 'Failed') { exit 1 } ; exit 0 } else { Stop-Job $job -Force; Write-Error 'timeout exceeded'; exit 124 }", command, secs)
	return powershellCommand(ps)
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
