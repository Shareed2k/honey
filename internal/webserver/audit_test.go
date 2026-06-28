package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/approval"
	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
)

// Ensure context is used (for captureSink.Log signature and noop test).
var _ = context.Background

// captureSink is a thread-safe audit.Sink that stores every event for inspection in tests.
type captureSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (c *captureSink) Log(_ context.Context, e audit.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *captureSink) Close() error { return nil }

func (c *captureSink) all() []audit.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Event, len(c.events))
	copy(out, c.events)
	return out
}

func decideApproval(t *testing.T, s *Server, id, decision string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(approvalDecisionRequest{Decision: decision})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/approvals/"+id, bytes.NewReader(body))
	// DisableAuth is set by newTestServer so no token needed; actor defaults to "".
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, r)
	return w
}

func TestHandleDecideApproval_emitsAuditEvent_approve(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	store := approval.NewStore(time.Hour)
	pending := store.Create("alice", "deploy.cue", []string{"prod-1"}, "risky")

	s := newTestServer(t, Options{AuditSink: sink, Approvals: store})
	w := decideApproval(t, s, pending.ID, "approve")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	e := events[0]
	if e.Action != "approval_decided" {
		t.Errorf("Action = %q, want %q", e.Action, "approval_decided")
	}
	if e.Decision != "allow" {
		t.Errorf("Decision = %q, want %q", e.Decision, "allow")
	}
	if e.ApprovalID != pending.ID {
		t.Errorf("ApprovalID = %q, want %q", e.ApprovalID, pending.ID)
	}
	if e.Source != "web" {
		t.Errorf("Source = %q, want %q", e.Source, "web")
	}
}

func TestHandleDecideApproval_emitsAuditEvent_deny(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	store := approval.NewStore(time.Hour)
	pending := store.Create("alice", "deploy.cue", []string{"prod-1"}, "risky")

	s := newTestServer(t, Options{AuditSink: sink, Approvals: store})
	w := decideApproval(t, s, pending.ID, "deny")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	e := events[0]
	if e.Decision != "deny" {
		t.Errorf("Decision = %q, want %q", e.Decision, "deny")
	}
	if e.ApprovalID != pending.ID {
		t.Errorf("ApprovalID = %q, want %q", e.ApprovalID, pending.ID)
	}
}

func TestHandleDecideApproval_noAuditOnError(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	store := approval.NewStore(time.Hour)

	s := newTestServer(t, Options{AuditSink: sink, Approvals: store})
	w := decideApproval(t, s, "nonexistent", "approve")

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
	if n := len(sink.all()); n != 0 {
		t.Errorf("expected 0 audit events on error, got %d", n)
	}
}

func TestNewServer_nilAuditSinkBecomesNoop(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, Options{})
	// AuditSink must never be nil after construction — any call should not panic.
	if err := s.opts.AuditSink.Log(context.Background(), audit.Event{Action: "test"}); err != nil {
		t.Errorf("noop sink Log: unexpected error: %v", err)
	}
}

// fakeRunner satisfies recipeRunnerIface for audit-event tests. Execute returns
// the pre-configured channel and error; DryRun returns a static plan.
type fakeRunner struct {
	ch  <-chan engine.HostExecResult
	err error
}

func (f *fakeRunner) DryRun(_ context.Context, _ engine.RunRequest) (string, error) {
	return "dry-run plan", nil
}

func (f *fakeRunner) Execute(_ context.Context, _ engine.RunRequest) (<-chan engine.HostExecResult, error) {
	return f.ch, f.err
}
func (f *fakeRunner) ExecuteAndWait(_ context.Context, _ engine.RunRequest) error { return nil }
func (f *fakeRunner) AssessCommandRisk(_ context.Context, _ engine.RunRequest) []engine.StepRisk {
	return nil
}

func closedResultChan() <-chan engine.HostExecResult {
	ch := make(chan engine.HostExecResult)
	close(ch)
	return ch
}

func cueExecBody(recipeName string, execute bool) string {
	return `{"recipe_content":{"name":"` + recipeName + `","steps":[{"host":"*","command":"echo hi"}]},"execute":` +
		func() string {
			if execute {
				return "true"
			}
			return "false"
		}() +
		`,"ssh_user":"ops","records":[{"provider":"static","name":"h1","primary_ip":"1.1.1.1"}]}`
}

func TestHandleCueExec_emitsAuditEvent_allow(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	s := newTestServer(t, Options{AuditSink: sink})
	s.recipesAPI.runner = &fakeRunner{ch: closedResultChan()}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(cueExecBody("myrecipe", true)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d (status=%d body=%s)", len(events), w.Code, w.Body)
	}
	e := events[0]
	if e.Action != "recipe_run" {
		t.Errorf("Action = %q, want %q", e.Action, "recipe_run")
	}
	if e.Decision != "allow" {
		t.Errorf("Decision = %q, want %q", e.Decision, "allow")
	}
	if e.Target != "myrecipe" {
		t.Errorf("Target = %q, want %q", e.Target, "myrecipe")
	}
	if e.Source != "web" {
		t.Errorf("Source = %q, want %q", e.Source, "web")
	}
}

func TestHandleCueExec_emitsAuditEvent_requireApproval(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	s := newTestServer(t, Options{AuditSink: sink})
	pendingErr := &engine.ErrPendingApproval{ID: "appr-abc", Reason: "risky op"}
	s.recipesAPI.runner = &fakeRunner{err: pendingErr}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(cueExecBody("deploy.cue", true)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body)
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	e := events[0]
	if e.Action != "recipe_run" {
		t.Errorf("Action = %q, want %q", e.Action, "recipe_run")
	}
	if e.Decision != "require_approval" {
		t.Errorf("Decision = %q, want %q", e.Decision, "require_approval")
	}
	if e.ApprovalID != "appr-abc" {
		t.Errorf("ApprovalID = %q, want %q", e.ApprovalID, "appr-abc")
	}
	if e.Target != "deploy.cue" {
		t.Errorf("Target = %q, want %q", e.Target, "deploy.cue")
	}
}

func TestHandleCueExec_noAuditEvent_dryRun(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	s := newTestServer(t, Options{AuditSink: sink})
	s.recipesAPI.runner = &fakeRunner{} // never called for dry-run

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(cueExecBody("myrecipe", false)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	if n := len(sink.all()); n != 0 {
		t.Errorf("expected 0 audit events for dry-run, got %d", n)
	}
}

// Ensure fakeRunner satisfies the interface at compile time.
var _ recipeRunnerIface = (*fakeRunner)(nil)

// Ensure cuetry is used (Recipe is referenced in handler).
var _ = cuetry.Recipe{}
