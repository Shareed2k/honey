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

func TestHandleExecOnHost_nilEnforcerDeniedByDefault(t *testing.T) {
	// Without an OPA enforcer and without HONEY_EXEC_ALLOW_UNVERIFIED, non-critical
	// commands must be denied (secure-by-default on the AI exec path).
	called := withFakeExec(t)
	withEnforcer(t, nil)
	t.Setenv(execAllowUnverifiedEnv, "")

	in := execOnHostInput{Host: "10.0.0.1", Name: "web1", Command: "whoami"}
	_, _, err := handleExecOnHost(context.Background(), nil, in)
	if err == nil {
		t.Fatal("nil enforcer + no env var must deny exec")
	}
	if *called {
		t.Fatal("SSH must NOT be called when exec is denied by default")
	}
}

func TestHandleExecOnHost_allowUnverifiedEnvUnlocksExec(t *testing.T) {
	// HONEY_EXEC_ALLOW_UNVERIFIED=1 lets benign commands run without an OPA enforcer.
	called := withFakeExec(t)
	withEnforcer(t, nil)
	t.Setenv(execAllowUnverifiedEnv, "1")

	in := execOnHostInput{Host: "10.0.0.1", Name: "web1", Command: "whoami"}
	_, out, err := handleExecOnHost(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("benign command with env var should run: %v", err)
	}
	if !*called {
		t.Fatal("SSH should be called when env var unlocks exec")
	}
	if len(out.Results) != 1 || out.Results[0].Output != "ok" {
		t.Fatalf("out=%+v", out)
	}
}

func TestHandleExecOnHost_enforcerAllowsExec(t *testing.T) {
	// A real OPA enforcer (allow policy) must reach execSSH.
	called := withFakeExec(t)
	enf, err := policy.NewFromSource(context.Background(), "p.rego", `package honey
import rego.v1
default allow := true
`)
	if err != nil {
		t.Fatal(err)
	}
	withEnforcer(t, enf)

	in := execOnHostInput{Host: "10.0.0.1", Name: "web1", Command: "whoami"}
	_, out, err := handleExecOnHost(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("enforcer-allowed command should run: %v", err)
	}
	if !*called {
		t.Fatal("SSH should be called for an allowed command")
	}
	if len(out.Results) != 1 || out.Results[0].Output != "ok" {
		t.Fatalf("out=%+v", out)
	}
}
