package cmdgate_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// The shipped example policies are the only guardrails a fresh honey install
// has (OPA is the sole command gate), so they are exercised against the REAL
// input builders rather than hand-written inputs that can drift away from what
// honey actually sends. Two failures this catches:
//
//   - a rule that reads a field the caller never sends (input.command is a
//     plain string on the interactive-terminal path, an object elsewhere), so
//     the guardrail silently allows everything;
//   - two `deny_reason :=` rules that can both hold for one command, which
//     makes OPA fail evaluation with "complete rules must not produce multiple
//     outputs" instead of denying.
const (
	exampleGuardrailDir = "../../examples/policy/command-guardrail"
	exampleMCPDir       = "../../examples/policy/mcp"

	// Trips the destructive-command rule without being a critical-severity
	// classification by itself.
	destructiveCmd = "dd if=/dev/zero of=/dev/disk0"
)

func prodRecord() hosts.Record {
	return hosts.Record{Name: "web1", Provider: "gcp", Meta: map[string]string{"env": "prod"}}
}

func TestExampleCommandGuardrail(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enforcer, err := policy.New(ctx, exampleGuardrailDir, nil)
	require.NoError(t, err)

	t.Run("interactive terminal shape (command is a string)", func(t *testing.T) {
		t.Parallel()
		// cmdgate.CommandPolicyInput is what the SSH gateway and the web/share
		// terminal guard send: `command` is a bare string.
		d, err := enforcer.Evaluate(ctx, cmdgate.CommandPolicyInput("alice", prodRecord(), destructiveCmd))
		require.NoError(t, err)
		require.False(t, d.Allow, "a destructive command on a prod target must be denied on the terminal path too")
		require.NotEmpty(t, d.DenyReason)
	})

	t.Run("exec/MCP shape with both rules tripping", func(t *testing.T) {
		t.Parallel()
		// Critical severity AND a destructive command on prod: both rules hold,
		// which is exactly the combination that used to fail evaluation.
		d, err := enforcer.Evaluate(ctx, map[string]any{
			"action": "command_exec",
			"actor":  "alice",
			"command": map[string]any{
				"raw":          destructiveCmd,
				"max_severity": "critical",
				"detected":     map[string]any{"commands": []string{"dd"}},
			},
			"target": map[string]any{"name": "web1", "env": "prod"},
		})
		require.NoError(t, err, "overlapping rules must not fail evaluation")
		require.False(t, d.Allow)
		require.Contains(t, d.DenyReason, "critical")
		require.Contains(t, d.DenyReason, "production")
	})

	t.Run("recipe step shape (raw and interpreter only)", func(t *testing.T) {
		t.Parallel()
		// internal/engine/risk_assess.go sends no risk fields at all.
		d, err := enforcer.Evaluate(ctx, map[string]any{
			"action":  "command_exec",
			"actor":   "alice",
			"command": map[string]any{"raw": destructiveCmd, "interpreter": ""},
			"target":  map[string]any{"name": "web1", "env": "prod"},
		})
		require.NoError(t, err)
		require.False(t, d.Allow)
	})

	t.Run("harmless command on a non-prod target is allowed", func(t *testing.T) {
		t.Parallel()
		rec := hosts.Record{Name: "dev1", Provider: "gcp", Meta: map[string]string{"env": "dev"}}
		d, err := enforcer.Evaluate(ctx, cmdgate.CommandPolicyInput("alice", rec, "uptime"))
		require.NoError(t, err)
		require.True(t, d.Allow)
		require.Empty(t, d.DenyReason)
	})
}

func TestExampleMCPPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	enforcer, err := policy.New(ctx, exampleMCPDir, nil)
	require.NoError(t, err)

	mcpInput := func(raw, severity string) map[string]any {
		return map[string]any{
			"action": "mcp_exec",
			"actor":  "mcp",
			"command": map[string]any{
				"raw":          raw,
				"max_severity": severity,
				"detected":     map[string]any{"commands": []string{}},
			},
			"target": map[string]any{"name": "web1", "env": "prod"},
		}
	}

	t.Run("read-only command allowed", func(t *testing.T) {
		t.Parallel()
		d, err := enforcer.Evaluate(ctx, mcpInput("uptime", "low"))
		require.NoError(t, err)
		require.True(t, d.Allow)
	})

	t.Run("critical rm trips both rules without failing evaluation", func(t *testing.T) {
		t.Parallel()
		d, err := enforcer.Evaluate(ctx, mcpInput("rm -f /etc/hosts", "critical"))
		require.NoError(t, err, "overlapping rules must not fail evaluation")
		require.False(t, d.Allow)
		require.Contains(t, d.DenyReason, "critical")
		require.Contains(t, d.DenyReason, "rm is forbidden")
	})

	t.Run("anything not on the allowlist is denied", func(t *testing.T) {
		t.Parallel()
		d, err := enforcer.Evaluate(ctx, mcpInput("systemctl restart nginx", "medium"))
		require.NoError(t, err)
		require.False(t, d.Allow)
	})
}
