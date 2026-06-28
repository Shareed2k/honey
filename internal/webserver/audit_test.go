package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/approval"
	"github.com/shareed2k/honey/internal/audit"
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
