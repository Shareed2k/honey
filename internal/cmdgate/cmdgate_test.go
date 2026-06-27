package cmdgate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/policy"
)

func TestDecide_criticalDeniesWithoutEnforcer(t *testing.T) {
	a := commandrisk.AnalyzeStep("mkfs.ext4 /dev/sda", "")
	reason, denied, err := cmdgate.Decide(context.Background(), nil, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("critical command must be denied even with nil enforcer")
	}
	if !strings.Contains(reason, "command risk") {
		t.Fatalf("reason=%q", reason)
	}
}

func TestDecide_benignAllowedWithoutEnforcer(t *testing.T) {
	a := commandrisk.AnalyzeStep("echo hi", "")
	reason, denied, err := cmdgate.Decide(context.Background(), nil, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if denied {
		t.Fatalf("benign command must be allowed, reason=%q", reason)
	}
}

func mustEnforcer(t *testing.T, src string) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "test.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	return enf
}

func TestDecide_enforcerDeny(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := false
deny_reason := "nope" if not allow
`)
	a := commandrisk.AnalyzeStep("echo hi", "")
	reason, denied, err := cmdgate.Decide(context.Background(), enf, a, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !denied || !strings.Contains(reason, "nope") {
		t.Fatalf("denied=%v reason=%q", denied, reason)
	}
}

func TestDecide_enforcerRequireApprovalDenies(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := true
decision := "require_approval"
`)
	a := commandrisk.AnalyzeStep("echo hi", "")
	_, denied, err := cmdgate.Decide(context.Background(), enf, a, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Fatal("require_approval must deny in non-interactive path")
	}
}

func TestDecide_enforcerAllow(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := true
`)
	a := commandrisk.AnalyzeStep("echo hi", "")
	_, denied, err := cmdgate.Decide(context.Background(), enf, a, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if denied {
		t.Fatal("allow policy on benign command must allow")
	}
}
