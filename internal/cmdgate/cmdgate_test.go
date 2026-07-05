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

func TestAssessTargets_criticalDeniesAllTargets(t *testing.T) {
	inputs := []cmdgate.TargetInput{
		{Name: "host-a", PolicyInput: map[string]any{"actor": "api"}},
		{Name: "host-b", PolicyInput: map[string]any{"actor": "api"}},
	}
	_, decisions, err := cmdgate.AssessTargets(context.Background(), nil, "mkfs.ext4 /dev/sda", "", inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("want 2 decisions, got %d", len(decisions))
	}
	for _, d := range decisions {
		if !d.Denied {
			t.Errorf("host %q: critical command must be denied, reason=%q", d.Name, d.Reason)
		}
	}
}

func TestAssessTargets_summaryOnlyUsesFirstTarget(t *testing.T) {
	inputs := []cmdgate.TargetInput{
		{Name: "host-a", PolicyInput: map[string]any{"actor": "api"}},
		{Name: "host-b", PolicyInput: map[string]any{"actor": "api"}},
	}
	_, decisions, err := cmdgate.AssessTargets(context.Background(), nil, "echo hi", "", inputs, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 {
		t.Fatalf("summaryOnly=true must return 1 decision, got %d", len(decisions))
	}
	if decisions[0].Name != "host-a" {
		t.Fatalf("summaryOnly=true must use first target, got %q", decisions[0].Name)
	}
}

func TestAssessTargets_emptyTargets(t *testing.T) {
	_, decisions, err := cmdgate.AssessTargets(context.Background(), nil, "echo hi", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 0 {
		t.Fatalf("want 0 decisions for nil targets, got %d", len(decisions))
	}
}

func TestAssessTargets_enforcerDenyPerTarget(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := false
deny_reason := "blocked" if not allow
`)
	inputs := []cmdgate.TargetInput{
		{Name: "host-a", PolicyInput: map[string]any{"actor": "api"}},
		{Name: "host-b", PolicyInput: map[string]any{"actor": "api"}},
	}
	_, decisions, err := cmdgate.AssessTargets(context.Background(), enf, "echo hi", "", inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("want 2 decisions, got %d", len(decisions))
	}
	for _, d := range decisions {
		if !d.Denied || !strings.Contains(d.Reason, "blocked") {
			t.Errorf("host %q: denied=%v reason=%q", d.Name, d.Denied, d.Reason)
		}
	}
}

func TestAssessTargets_analysisReturnedOnce(t *testing.T) {
	inputs := []cmdgate.TargetInput{
		{Name: "host-a", PolicyInput: nil},
		{Name: "host-b", PolicyInput: nil},
	}
	analysis, _, err := cmdgate.AssessTargets(context.Background(), nil, "mkfs.ext4 /dev/sda", "", inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.FirstCritical() == nil {
		t.Fatal("analysis must capture critical signal for mkfs.ext4")
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
