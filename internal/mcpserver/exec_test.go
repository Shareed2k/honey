package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// withFakeExec swaps execSSH for a recorder and restores it after the test.
func withFakeExec(t *testing.T) *bool {
	t.Helper()
	called := false
	prev := execSSH
	execSSH = func(_ string, recs []hosts.Record, _ func(hosts.Record) string, _ int, _ hostexec.Registry) ([]engine.HostExecResult, error) {
		called = true
		name := ""
		ip := ""
		if len(recs) > 0 {
			name, ip = recs[0].Name, recs[0].PrimaryIP
		}
		return []engine.HostExecResult{{Name: name, IP: ip, Output: "ok", ExitCode: 0}}, nil
	}
	t.Cleanup(func() { execSSH = prev })
	return &called
}

func withEnforcer(t *testing.T, enf *policy.Enforcer) {
	t.Helper()
	prev := policyEnforcer
	policyEnforcer = enf
	t.Cleanup(func() { policyEnforcer = prev })
}

func TestHandleExecOnHost_criticalBlockedWithoutEnforcer(t *testing.T) {
	called := withFakeExec(t)
	withEnforcer(t, nil)

	in := execOnHostInput{Host: "10.0.0.1", Command: "mkfs.ext4 /dev/sda"}
	_, _, err := handleExecOnHost(context.Background(), nil, in)
	if err == nil {
		t.Fatal("expected critical command to be blocked")
	}
	if !strings.Contains(err.Error(), "command risk") {
		t.Fatalf("err=%v", err)
	}
	if *called {
		t.Fatal("SSH must NOT be called for a blocked command")
	}
}

func TestHandleExecOnHost_requireApprovalBlocked(t *testing.T) {
	called := withFakeExec(t)
	enf, err := policy.NewFromSource(context.Background(), "p.rego", `package honey
import rego.v1
default allow := true
decision := "require_approval"
`)
	if err != nil {
		t.Fatal(err)
	}
	withEnforcer(t, enf)

	in := execOnHostInput{Host: "10.0.0.1", Command: "whoami"}
	_, _, err = handleExecOnHost(context.Background(), nil, in)
	if err == nil {
		t.Fatal("require_approval must block in non-interactive MCP path")
	}
	if *called {
		t.Fatal("SSH must NOT be called when approval required")
	}
}

func TestHandleExecOnHost_allowExecutes(t *testing.T) {
	called := withFakeExec(t)
	withEnforcer(t, nil) // nil enforcer allows non-critical commands

	in := execOnHostInput{Host: "10.0.0.1", Name: "web1", Command: "whoami"}
	_, out, err := handleExecOnHost(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("benign command should run: %v", err)
	}
	if !*called {
		t.Fatal("SSH should be called for an allowed command")
	}
	if len(out.Results) != 1 || out.Results[0].Output != "ok" {
		t.Fatalf("out=%+v", out)
	}
}
