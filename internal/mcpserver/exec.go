package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

// execSSH runs the command over SSH. It is a package variable so tests can
// substitute a fake that records calls without touching real hosts.
var execSSH = engine.ExecuteSSHParallel

// riskDisableEnv bypasses the command-risk gate entirely, including the OPA
// decision, for trusted automation. Mirrors the engine's escape hatch.
const riskDisableEnv = "HONEY_RISK_DISABLE"

func riskGateDisabled() bool {
	v := strings.TrimSpace(os.Getenv(riskDisableEnv))
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// execAllowUnverifiedEnv opts the MCP exec path into "allow commands without
// an OPA enforcer". Without this env var, exec_on_host requires either a
// configured OPA enforcer (HONEY_POLICY_DIR) or an explicit opt-in here —
// independent of command risk, which is data the policy can act on, never a
// gate by itself.
const execAllowUnverifiedEnv = "HONEY_EXEC_ALLOW_UNVERIFIED"

func execUnverifiedAllowed() bool {
	v := strings.TrimSpace(os.Getenv(execAllowUnverifiedEnv))
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

type execOnHostInput struct {
	// Host is the IP address or hostname to connect to directly via SSH.
	// Use the primary_ip or name from a prior search_hosts call.
	Host       string `json:"host"                  mod:"trim"           validate:"required"`
	Name       string `json:"name,omitempty"        mod:"trim"`
	Command    string `json:"command"               mod:"trim"           validate:"required"`
	Shell      string `json:"shell,omitempty"       mod:"trim,lcase"     validate:"omitempty,oneof=bash sh"`
	TimeoutSec int    `json:"timeout_sec,omitempty"                      validate:"min=0,max=3600"`
}

type execOnHostOutput struct {
	Results []execHostResult `json:"results"`
}

type execHostResult struct {
	Host     string `json:"host"`
	IP       string `json:"ip"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func handleExecOnHost(ctx context.Context, _ *mcp.CallToolRequest, in execOnHostInput) (*mcp.CallToolResult, execOnHostOutput, error) {
	if err := conform.Struct(ctx, &in); err != nil {
		return nil, execOnHostOutput{}, err
	}
	if err := validate.Struct(in); err != nil {
		return nil, execOnHostOutput{}, err
	}

	name := in.Name
	if name == "" {
		name = in.Host
	}
	record := hosts.Record{Name: name, PrimaryIP: in.Host}

	// Gate via deny-by-default + OPA enforcer (honey's only command-authorization
	// gate). Non-critical and critical commands alike require either a configured
	// OPA enforcer or HONEY_EXEC_ALLOW_UNVERIFIED=1; commandrisk severity is data
	// fed to the policy, never a gate by itself.
	if !riskGateDisabled() {
		reason, denied, gerr := gateMCPExec(ctx, in.Command, in.Shell, record)
		if gerr != nil {
			return nil, execOnHostOutput{}, gerr
		}
		evt := audit.Event{
			Actor:   mcpActor,
			Source:  "mcp",
			Action:  "exec",
			Target:  record.Name,
			Command: in.Command,
		}
		if denied {
			evt.Decision = "deny"
			evt.DenyReason = reason
			_ = auditSink.Log(ctx, evt)
			return nil, execOnHostOutput{}, fmt.Errorf("blocked: %s", reason)
		}
		evt.Decision = "allow"
		_ = auditSink.Log(ctx, evt)
	}

	cmd := buildShellCmd(in.Command, in.Shell)
	recordDir := ""
	if serverCfg != nil {
		recordDir = serverCfg.Defaults.RecordDir
	}

	rawResults, err := execSSH("", []engine.TargetContext{{Record: record}}, func(_ hosts.Record) string { return cmd }, 8, globalExecReg)
	if err != nil {
		return nil, execOnHostOutput{}, fmt.Errorf("ssh exec: %w", err)
	}

	out := execOnHostOutput{Results: make([]execHostResult, 0, len(rawResults))}
	for _, r := range rawResults {
		res := execHostResult{
			Host:     r.Name,
			IP:       r.IP,
			Output:   r.Output,
			ExitCode: r.ExitCode,
		}
		if r.ErrMsg != "" {
			res.Error = r.ErrMsg
		}
		out.Results = append(out.Results, res)

		// Session recording: if record_dir configured, write a recording file per host.
		if recordDir != "" {
			recordExecResult(recordDir, r)
		}
	}
	return nil, out, nil
}

// gateMCPExec analyzes the raw command and decides whether it may run, building
// the policy input for the "mcp_exec" action. Returns (reason, denied, err).
//
// Decision order:
//  1. Deny-by-default: no OPA enforcer + no HONEY_EXEC_ALLOW_UNVERIFIED → deny.
//     This is independent of command risk — it requires an explicit opt-in
//     (policy or env var) before the AI exec path runs anything at all.
//  2. OPA enforcer evaluation via cmdgate.Decide. commandrisk severity feeds
//     the policy as data (input.command.max_severity/riskSignals/detected) but
//     never denies by itself — OPA is honey's only command-authorization gate.
func gateMCPExec(ctx context.Context, rawCommand, interpreter string, t hosts.Record) (string, bool, error) {
	if strings.TrimSpace(rawCommand) == "" {
		return "", false, nil
	}
	analysis := commandrisk.AnalyzeStep(rawCommand, interpreter)

	// Step 1: deny-by-default when no OPA enforcer is wired in.
	if policyEnforcer == nil && !execUnverifiedAllowed() {
		return "exec_on_host requires a policy enforcer (set HONEY_POLICY_DIR) " +
			"or set HONEY_EXEC_ALLOW_UNVERIFIED=1 to allow execution without a policy", true, nil
	}

	// Step 2: OPA contextual evaluation.
	input := map[string]any{
		"action": "mcp_exec",
		"actor":  mcpActor,
		"command": map[string]any{
			"raw":          rawCommand,
			"riskSignals":  analysis.Signals,
			"detected":     analysis.Detected,
			"max_severity": string(analysis.MaxSeverity),
			"interpreter":  analysis.Interpreter,
		},
		"target": map[string]any{
			"name":     t.Name,
			"provider": t.Provider,
		},
	}
	res, err := cmdgate.Decide(ctx, policyEnforcer, input)
	if err != nil {
		return "", false, err
	}
	return res.Reason, res.Denied, nil
}

// buildShellCmd wraps the command with an explicit interpreter when shell is set.
func buildShellCmd(cmd, shell string) string {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		var b bytes.Buffer
		b.WriteString("bash -c ")
		b.WriteString(shellQuote(cmd))
		return b.String()
	case "sh":
		var b bytes.Buffer
		b.WriteString("sh -c ")
		b.WriteString(shellQuote(cmd))
		return b.String()
	default:
		return cmd
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func recordExecResult(recordDir string, r engine.HostExecResult) {
	rec, err := engine.NewSessionRecorder(engine.SessionRecorderOptions{
		Dir:      recordDir,
		Trigger:  "mcp",
		Mode:     "exec",
		HostName: r.Name,
		HostIP:   r.IP,
	})
	if err != nil {
		return
	}
	rec.RecordHostExecResult(r)
	_ = rec.Close()
}
