package mcpserver

import (
	"context"
	"strings"
	"testing"
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

func TestHandlePlanCommand_criticalCommand_deny(t *testing.T) {
	withEnforcer(t, nil)
	in := planCommandInput{Command: "rm -rf /"}
	_, out, err := handlePlanCommand(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", out.Decision)
	}
	if string(out.Risk) != "critical" {
		t.Errorf("Risk = %q, want critical", out.Risk)
	}
	if len(out.Signals) == 0 {
		t.Errorf("expected signals, got none")
	}
	if out.Reason == "" {
		t.Errorf("expected non-empty Reason for critical deny")
	}
}

func TestHandlePlanCommand_criticalCommand_signalsPresent(t *testing.T) {
	withEnforcer(t, nil)
	in := planCommandInput{Command: "dd if=/dev/urandom of=/dev/sda"}
	_, out, err := handlePlanCommand(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", out.Decision)
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
	if out.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", out.Decision)
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
