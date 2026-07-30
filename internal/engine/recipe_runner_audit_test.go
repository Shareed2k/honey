package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/approval"
	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/cuetry"
)

// captureAuditSink records the events admitRecipe emits so tests can assert the
// verdict the runner audited.
type captureAuditSink struct{ events []audit.Event }

func (c *captureAuditSink) Log(_ context.Context, e audit.Event) error {
	c.events = append(c.events, e)
	return nil
}

func (c *captureAuditSink) Close() error { return nil }

func (c *captureAuditSink) one(t *testing.T) audit.Event {
	t.Helper()
	if len(c.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d: %+v", len(c.events), c.events)
	}
	return c.events[0]
}

func auditReq() RunRequest {
	return RunRequest{Source: "web", ActorID: "alice", Recipe: cuetry.Recipe{Name: "r1"}}
}

// admitRecipe is the single place recipe-run admission is decided, so it is also
// where the recipe_run audit event is emitted — one hook covering web, webhook,
// and scheduler instead of per-handler blocks. These tests assert each verdict
// audits the right Decision, including require_biometric, which the old
// per-handler audit blocks never recorded.

func TestAdmitRecipe_audits_allowNoEnforcer(t *testing.T) {
	sink := &captureAuditSink{}
	r := NewRecipeRunner(RunnerOptions{AuditSink: sink})
	if err := r.admitRecipe(context.Background(), auditReq()); err != nil {
		t.Fatalf("admitRecipe: %v", err)
	}
	e := sink.one(t)
	if e.Action != "recipe_run" || e.Decision != "allow" || e.Source != "web" || e.Target != "r1" || e.Actor != "alice" {
		t.Fatalf("event = %+v", e)
	}
}

func TestAdmitRecipe_audits_requireBiometric(t *testing.T) {
	// require_biometric with no BiometricVerifier hard-denies. This verdict was
	// NOT audited before the audit hook moved into the runner (the closed gap).
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := false
decision := "require_biometric"
`)
	sink := &captureAuditSink{}
	r := NewRecipeRunner(RunnerOptions{Enforcer: enf, AuditSink: sink})
	if err := r.admitRecipe(context.Background(), auditReq()); err == nil {
		t.Fatal("expected biometric hard-deny")
	}
	if e := sink.one(t); e.Decision != "require_biometric" {
		t.Fatalf("decision = %q, want require_biometric", e.Decision)
	}
}

func TestAdmitRecipe_audits_requireApproval(t *testing.T) {
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := false
decision := "require_approval"
`)
	sink := &captureAuditSink{}
	r := NewRecipeRunner(RunnerOptions{Enforcer: enf, Approvals: approval.NewStore(time.Hour), AuditSink: sink})
	err := r.admitRecipe(context.Background(), auditReq())
	var pending *ErrPendingApproval
	if !errors.As(err, &pending) {
		t.Fatalf("expected ErrPendingApproval, got %v", err)
	}
	e := sink.one(t)
	if e.Decision != "require_approval" || e.ApprovalID != pending.ID {
		t.Fatalf("event = %+v (pending id %q)", e, pending.ID)
	}
}

func TestAdmitRecipe_audits_deny(t *testing.T) {
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := false
default deny_reason := "nope"
`)
	sink := &captureAuditSink{}
	r := NewRecipeRunner(RunnerOptions{Enforcer: enf, AuditSink: sink})
	if err := r.admitRecipe(context.Background(), auditReq()); err == nil {
		t.Fatal("expected deny")
	}
	if e := sink.one(t); e.Decision != "deny" {
		t.Fatalf("decision = %q, want deny", e.Decision)
	}
}

func TestAdmitRecipe_nilSink_noPanic(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{}) // no sink, no enforcer
	if err := r.admitRecipe(context.Background(), auditReq()); err != nil {
		t.Fatalf("admitRecipe: %v", err)
	}
}
