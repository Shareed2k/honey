package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/shareed2k/honey/internal/aichat"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// runExecCheck analyzes a command's risk and prints the decision without
// executing. With HONEY_POLICY_DIR set it also runs the OPA command_exec policy
// per target. Returns a non-nil error (non-zero exit) when the command is a
// built-in critical pattern or any target is denied by policy.
func runExecCheck(ctx context.Context, command string, jobs []hosts.Record) error {
	a := commandrisk.Analyze(command)

	fmt.Fprintf(os.Stdout, "Command: %s\n", command)
	if a.MaxSeverity == "" {
		fmt.Fprintln(os.Stdout, "Risk: none")
	} else {
		fmt.Fprintf(os.Stdout, "Risk: %s\n", a.MaxSeverity)
	}
	for _, s := range a.Signals {
		fmt.Fprintf(os.Stdout, "  - [%s] %s: %s\n", s.Severity, s.ID, s.Reason)
	}
	if len(a.Detected.Commands) > 0 {
		fmt.Fprintf(os.Stdout, "Detected: commands=%v flags=%v paths=%v\n", a.Detected.Commands, a.Detected.Flags, a.Detected.Paths)
	}

	if flagExecShellchk {
		printShellcheck(command)
	}

	printLLMAdvice(ctx, command, a)

	denied := a.Critical
	if a.Critical {
		fmt.Fprintf(os.Stdout, "Decision: DENY (built-in critical: %s)\n", a.FirstCritical().Reason)
	}

	if enf := checkEnforcer(ctx); enf != nil {
		denied = evalCheckPolicy(ctx, enf, command, a, jobs) || denied
	} else if !a.Critical {
		fmt.Fprintln(os.Stdout, "Decision: allow (no policy configured; only built-in critical patterns deny)")
	}

	if denied {
		return fmt.Errorf("command risk check failed")
	}
	return nil
}

// checkEnforcer builds an OPA enforcer from HONEY_POLICY_DIR (+ config inventory),
// or returns nil when no policy dir is configured.
func checkEnforcer(ctx context.Context) *policy.Enforcer {
	dir := strings.TrimSpace(os.Getenv(policyDirEnv))
	if dir == "" {
		return nil
	}
	data, err := inventoryData(resolvedCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "policy inventory: %v\n", err)
		return nil
	}
	enf, err := policy.New(ctx, dir, data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "policy load: %v\n", err)
		return nil
	}
	return enf
}

// evalCheckPolicy runs the command_exec policy per target and prints each
// decision, returning true if any target was denied.
func evalCheckPolicy(ctx context.Context, enf *policy.Enforcer, command string, a commandrisk.Analysis, jobs []hosts.Record) bool {
	var denied bool
	for _, t := range jobs {
		input := map[string]any{
			"action": "command_exec",
			"actor":  "cli",
			"command": map[string]any{
				"raw": command, "riskSignals": a.Signals, "detected": a.Detected, "max_severity": string(a.MaxSeverity),
			},
			"target":    map[string]any{"name": t.Name, "provider": t.Provider, "env": t.Meta["env"], "groups": t.Groups},
			"execution": map[string]any{"dry_run": true},
		}
		d, err := enf.Evaluate(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "policy eval (%s): %v\n", t.Name, err)
			denied = true
			continue
		}
		verdict := d.Decision
		if verdict == "" {
			if d.Allow {
				verdict = "allow"
			} else {
				verdict = "deny"
			}
		}
		line := fmt.Sprintf("Policy[%s]: %s", t.Name, verdict)
		if d.DenyReason != "" {
			line += " — " + d.DenyReason
		}
		if len(d.Requires) > 0 {
			line += " (requires: " + strings.Join(d.Requires, ", ") + ")"
		}
		fmt.Fprintln(os.Stdout, line)
		if !d.Allow || verdict == "deny" || verdict == "require_approval" || verdict == "require_biometric" {
			denied = true
		}
	}
	return denied
}

// printLLMAdvice prints an advisory LLM classification when HONEY_RISK_LLM=1 and
// a model endpoint is reachable. It is advisory only and never affects the exit
// code; any error is reported and ignored.
func printLLMAdvice(ctx context.Context, command string, a commandrisk.Analysis) {
	if v := strings.TrimSpace(os.Getenv("HONEY_RISK_LLM")); v == "" || v == "0" || strings.EqualFold(v, "false") {
		return
	}
	advisor := commandrisk.NewLLMAdvisor(aichat.Complete, strings.TrimSpace(os.Getenv("OPENAI_MODEL")))
	advice, err := advisor.Advise(ctx, command, a.Detected)
	if err != nil {
		fmt.Fprintf(os.Stdout, "LLM advisory: unavailable (%v)\n", err)
		return
	}
	fmt.Fprintf(os.Stdout, "LLM advisory (not authoritative): risk=%s reasons=%v\n", advice.Risk, advice.Reasons)
	if advice.Explanation != "" {
		fmt.Fprintf(os.Stdout, "  %s\n", advice.Explanation)
	}
}

// printShellcheck runs shellcheck on the command when the binary is on PATH,
// printing its warnings. A missing binary or any error is non-fatal.
func printShellcheck(command string) {
	bin, err := exec.LookPath("shellcheck")
	if err != nil {
		fmt.Fprintln(os.Stdout, "ShellCheck: not installed (skipped)")
		return
	}
	script := "#!/bin/sh\n" + command + "\n"
	// #nosec G204 -- fixed binary, command flows via stdin not argv.
	cmd := exec.Command(bin, "--format=json", "-")
	cmd.Stdin = strings.NewReader(script)
	out, _ := cmd.Output()
	var findings []struct {
		Line    int    `json:"line"`
		Code    int    `json:"code"`
		Level   string `json:"level"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(out, &findings); err != nil || len(findings) == 0 {
		fmt.Fprintln(os.Stdout, "ShellCheck: no warnings")
		return
	}
	fmt.Fprintln(os.Stdout, "ShellCheck warnings:")
	for _, f := range findings {
		fmt.Fprintf(os.Stdout, "  - SC%d (%s) line %d: %s\n", f.Code, f.Level, f.Line, f.Message)
	}
}
