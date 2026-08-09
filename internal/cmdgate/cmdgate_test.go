package cmdgate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/guardrails"
	"github.com/shareed2k/honey/internal/policy"
)

func TestDecide_criticalDeniesWithoutEnforcer(t *testing.T) {
	a := commandrisk.AnalyzeStep("mkfs.ext4 /dev/sda", "")
	res, err := cmdgate.Decide(context.Background(), nil, nil, a, nil, "mkfs.ext4 /dev/sda", guardrails.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Denied {
		t.Fatal("critical command must be denied even with nil enforcer")
	}
	if !strings.Contains(res.Reason, "command risk") {
		t.Fatalf("reason=%q", res.Reason)
	}
}

func TestDecide_benignAllowedWithoutEnforcer(t *testing.T) {
	a := commandrisk.AnalyzeStep("echo hi", "")
	res, err := cmdgate.Decide(context.Background(), nil, nil, a, nil, "echo hi", guardrails.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied {
		t.Fatalf("benign command must be allowed, reason=%q", res.Reason)
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

func mustRuleset(t *testing.T, rules []guardrails.Rule) *guardrails.Ruleset {
	t.Helper()
	rs, err := guardrails.NewRuleset(rules)
	if err != nil {
		t.Fatalf("NewRuleset: %v", err)
	}
	return rs
}

func TestDecide_enforcerDeny(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := false
deny_reason := "nope" if not allow
`)
	a := commandrisk.AnalyzeStep("echo hi", "")
	res, err := cmdgate.Decide(context.Background(), enf, nil, a, map[string]any{"actor": "mcp"}, "echo hi", guardrails.Attrs{})
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
	a := commandrisk.AnalyzeStep("echo hi", "")
	res, err := cmdgate.Decide(context.Background(), enf, nil, a, map[string]any{"actor": "mcp"}, "echo hi", guardrails.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Denied {
		t.Fatal("require_approval must deny in non-interactive path")
	}
}

// TestDecide_guardrailDenyBlocksBeforeEnforcer proves a guardrail deny rule
// blocks a non-critical command even when the OPA enforcer would allow it — the
// guardrail is a deterministic floor evaluated before OPA.
func TestDecide_guardrailDenyBlocksBeforeEnforcer(t *testing.T) {
	enf := mustEnforcer(t, `package honey
import rego.v1
default allow := true
`)
	rs := mustRuleset(t, []guardrails.Rule{{
		Name:      "no-shutdown",
		Action:    guardrails.ActionDeny,
		AppliesTo: guardrails.KindCommand,
		Words:     []string{"shutdown"},
		Message:   "shutdown is blocked",
	}})
	a := commandrisk.AnalyzeStep("shutdown now", "")
	res, err := cmdgate.Decide(context.Background(), enf, rs, a, map[string]any{"actor": "mcp"}, "shutdown now", guardrails.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Denied || res.Reason != "shutdown is blocked" {
		t.Fatalf("guardrail deny expected, denied=%v reason=%q", res.Denied, res.Reason)
	}
}

// TestDecide_guardrailWarnAllowsAndReports proves a guardrail warn rule allows
// the command (with a nil enforcer) and surfaces its message as a warning.
func TestDecide_guardrailWarnAllowsAndReports(t *testing.T) {
	rs := mustRuleset(t, []guardrails.Rule{{
		Name:      "note-rm",
		Action:    guardrails.ActionWarn,
		AppliesTo: guardrails.KindCommand,
		Words:     []string{"rm "},
		Message:   "removing files",
	}})
	a := commandrisk.AnalyzeStep("rm foo.txt", "")
	res, err := cmdgate.Decide(context.Background(), nil, rs, a, nil, "rm foo.txt", guardrails.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied {
		t.Fatalf("warn rule must not deny, reason=%q", res.Reason)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "removing files" {
		t.Fatalf("warnings=%v, want [removing files]", res.Warnings)
	}
}

// TestDecide_nilRulesetIsNoOp proves a nil ruleset does not change the verdict
// for a benign command (behavior identical to the pre-guardrail gate).
func TestDecide_nilRulesetIsNoOp(t *testing.T) {
	a := commandrisk.AnalyzeStep("echo hi", "")
	res, err := cmdgate.Decide(context.Background(), nil, nil, a, nil, "echo hi", guardrails.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied || len(res.Warnings) != 0 {
		t.Fatalf("nil ruleset must be a no-op, got denied=%v warnings=%v", res.Denied, res.Warnings)
	}
}

func TestAssessTargets_criticalDeniesAllTargets(t *testing.T) {
	inputs := []cmdgate.TargetInput{
		{Name: "host-a", PolicyInput: map[string]any{"actor": "api"}},
		{Name: "host-b", PolicyInput: map[string]any{"actor": "api"}},
	}
	_, decisions, err := cmdgate.AssessTargets(context.Background(), nil, nil, "mkfs.ext4 /dev/sda", "", inputs, false)
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
	_, decisions, err := cmdgate.AssessTargets(context.Background(), nil, nil, "echo hi", "", inputs, true)
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
	_, decisions, err := cmdgate.AssessTargets(context.Background(), nil, nil, "echo hi", "", nil, false)
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
	_, decisions, err := cmdgate.AssessTargets(context.Background(), enf, nil, "echo hi", "", inputs, false)
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

// TestAssessTargets_guardrailDenyAndWarnPerTarget proves the guardrail floor
// applies per target through AssessTargets: a deny rule scoped to a target
// blocks only that target; a warn rule is reported on the allowed target.
func TestAssessTargets_guardrailDenyAndWarnPerTarget(t *testing.T) {
	rs := mustRuleset(t, []guardrails.Rule{
		{
			Name:      "prod-no-rm",
			Action:    guardrails.ActionDeny,
			AppliesTo: guardrails.KindCommand,
			Words:     []string{"rm "},
			Message:   "rm blocked in prod",
			Targets:   []string{"prod-*"},
		},
		{
			Name:      "note-rm",
			Action:    guardrails.ActionWarn,
			AppliesTo: guardrails.KindCommand,
			Words:     []string{"rm "},
			Message:   "removing files",
		},
	})
	inputs := []cmdgate.TargetInput{
		{Name: "prod-a", Attrs: guardrails.Attrs{Name: "prod-a"}},
		{Name: "dev-b", Attrs: guardrails.Attrs{Name: "dev-b"}},
	}
	_, decisions, err := cmdgate.AssessTargets(context.Background(), nil, rs, "rm foo.txt", "", inputs, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 {
		t.Fatalf("want 2 decisions, got %d", len(decisions))
	}
	if !decisions[0].Denied || decisions[0].Reason != "rm blocked in prod" {
		t.Fatalf("prod target must be denied by guardrail, got denied=%v reason=%q", decisions[0].Denied, decisions[0].Reason)
	}
	if decisions[1].Denied {
		t.Fatalf("dev target must be allowed, reason=%q", decisions[1].Reason)
	}
	if len(decisions[1].Warnings) != 1 || decisions[1].Warnings[0] != "removing files" {
		t.Fatalf("dev target warnings=%v, want [removing files]", decisions[1].Warnings)
	}
}

func TestAssessTargets_analysisReturnedOnce(t *testing.T) {
	inputs := []cmdgate.TargetInput{
		{Name: "host-a", PolicyInput: nil},
		{Name: "host-b", PolicyInput: nil},
	}
	analysis, _, err := cmdgate.AssessTargets(context.Background(), nil, nil, "mkfs.ext4 /dev/sda", "", inputs, false)
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
	res, err := cmdgate.Decide(context.Background(), enf, nil, a, map[string]any{"actor": "mcp"}, "echo hi", guardrails.Attrs{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Denied {
		t.Fatal("allow policy on benign command must allow")
	}
}
