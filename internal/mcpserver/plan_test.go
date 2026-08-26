package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/policy"
)

func TestHandlePlanCommand_safeCommand_allow(t *testing.T) {
	withEnforcer(t, nil)
	in := planCommandInput{Command: "echo hello"}
	_, out, err := handlePlanCommand(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("Decision = %q, want allow", out.Decision)
	}
	if string(out.Risk) == "critical" {
		t.Errorf("safe command reported critical risk")
	}
}

// TestHandlePlanCommand_criticalCommand_allowsWithoutEnforcer proves
// commandrisk severity is data, not a gate: with no OPA enforcer configured,
// a critical command still allows — only a configured policy can deny.
func TestHandlePlanCommand_criticalCommand_allowsWithoutEnforcer(t *testing.T) {
	withEnforcer(t, nil)
	in := planCommandInput{Command: "rm -rf /"}
	_, out, err := handlePlanCommand(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("Decision = %q, want allow (no OPA policy configured)", out.Decision)
	}
	if string(out.Risk) != "critical" {
		t.Errorf("Risk = %q, want critical", out.Risk)
	}
	if len(out.Signals) == 0 {
		t.Errorf("expected signals, got none")
	}
}

// TestHandlePlanCommand_criticalCommand_deniedByEnforcer proves a configured
// OPA policy can act on the severity commandrisk hands it and deny.
func TestHandlePlanCommand_criticalCommand_deniedByEnforcer(t *testing.T) {
	enf, err := policy.NewFromSource(context.Background(), "p.rego", `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "command_exec"
	input.command.max_severity == "critical"
}
deny_reason := "critical commands are blocked" if {
	input.action == "command_exec"
	input.command.max_severity == "critical"
}`)
	if err != nil {
		t.Fatal(err)
	}
	withEnforcer(t, enf)
	in := planCommandInput{Command: "rm -rf /"}
	_, out, herr := handlePlanCommand(context.Background(), nil, in)
	if herr != nil {
		t.Fatalf("unexpected error: %v", herr)
	}
	if out.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", out.Decision)
	}
	if out.Reason == "" {
		t.Errorf("expected non-empty Reason for policy deny")
	}
}

func TestHandlePlanCommand_criticalCommand_signalsPresent(t *testing.T) {
	withEnforcer(t, nil)
	in := planCommandInput{Command: "dd if=/dev/urandom of=/dev/sda"}
	_, out, err := handlePlanCommand(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("Decision = %q, want allow (no OPA policy configured)", out.Decision)
	}
	found := false
	for _, sig := range out.Signals {
		if sig.ID == "DD_WRITE_BLOCK_DEVICE" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected DD_WRITE_BLOCK_DEVICE signal, got %+v", out.Signals)
	}
}

func TestHandlePlanCommand_pythonInterpreter(t *testing.T) {
	withEnforcer(t, nil)
	in := planCommandInput{Command: `shutil.rmtree("/")`, Interpreter: "python3"}
	_, out, err := handlePlanCommand(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("Decision = %q, want allow (no OPA policy configured)", out.Decision)
	}
	if string(out.Risk) != "critical" {
		t.Errorf("Risk = %q, want critical", out.Risk)
	}
}

func TestHandlePlanCommand_emptyCommand_error(t *testing.T) {
	in := planCommandInput{Command: ""}
	_, _, err := handlePlanCommand(context.Background(), nil, in)
	if err == nil {
		t.Fatal("expected validation error for empty command")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Errorf("err = %v, want error mentioning command", err)
	}
}
