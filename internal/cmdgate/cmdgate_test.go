package cmdgate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/policy"
)

func mustEnforcer(t *testing.T, src string) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "test.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	return enf
}

// TestDecide_nilEnforcerAllowsCritical proves OPA is the only
// command-authorization gate: with no enforcer configured, even a
// commandrisk-critical command (mkfs.ext4, a destructive filesystem op) is
// allowed. commandrisk analysis is data a policy can act on, never a gate by
// itself.
func TestDecide_nilEnforcerAllowsCritical(t *testing.T) {
	res, err := cmdgate.Decide(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied {
		t.Fatalf("nil enforcer must allow unconditionally, got denied reason=%q", res.Reason)
	}
}

func TestDecide_nilEnforcerAllowsBenign(t *testing.T) {
	res, err := cmdgate.Decide(context.Background(), nil, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied {
		t.Fatalf("benign command must be allowed, reason=%q", res.Reason)
	}
}

func TestDecide_enforcerDeny(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := false
deny_reason := "nope" if not allow
`)
	res, err := cmdgate.Decide(context.Background(), enf, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Denied || !strings.Contains(res.Reason, "nope") {
		t.Fatalf("denied=%v reason=%q", res.Denied, res.Reason)
	}
}

func TestDecide_enforcerRequireApprovalDenies(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := true
decision := "require_approval"
`)
	res, err := cmdgate.Decide(context.Background(), enf, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Denied {
		t.Fatal("require_approval must deny in non-interactive path")
	}
}

func TestDecide_enforcerRequireBiometricDenies(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := true
decision := "require_biometric"
`)
	res, err := cmdgate.Decide(context.Background(), enf, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Denied {
		t.Fatal("require_biometric must deny in non-interactive path")
	}
}

func TestDecide_enforcerAllow(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := true
`)
	res, err := cmdgate.Decide(context.Background(), enf, map[string]any{"actor": "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied {
		t.Fatal("allow policy on benign command must allow")
	}
}

// TestDecide_enforcerCanAllowCriticalSeverity proves a policy can act on the
// severity commandrisk hands it (input.command.max_severity) and choose to
// allow anyway — commandrisk never overrides the policy's own verdict.
func TestDecide_enforcerCanAllowCriticalSeverity(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := true
`)
	input := map[string]any{
		"actor":   "mcp",
		"command": map[string]any{"raw": "mkfs.ext4 /dev/sda", "max_severity": "critical"},
	}
	res, err := cmdgate.Decide(context.Background(), enf, input)
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied {
		t.Fatalf("policy explicitly allowing must not be overridden by severity, reason=%q", res.Reason)
	}
}

func TestAssessTargets_nilEnforcerAllowsAllTargets(t *testing.T) {
	inputs := []cmdgate.TargetInput{
		{Name: "host-a", PolicyInput: map[string]any{"actor": "api"}},
		{Name: "host-b", PolicyInput: map[string]any{"actor": "api"}},
	}
	analysis, decisions, err := cmdgate.AssessTargets(context.Background(), nil, "mkfs.ext4 /dev/sda", "", inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.FirstCritical() == nil {
		t.Fatal("analysis must still capture the critical signal as data")
	}
	if len(decisions) != 2 {
		t.Fatalf("want 2 decisions, got %d", len(decisions))
	}
	for _, d := range decisions {
		if d.Denied {
			t.Errorf("host %q: nil enforcer must allow even a critical command, reason=%q", d.Name, d.Reason)
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
