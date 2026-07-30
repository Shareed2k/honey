package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/approval"
	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/queue"
	"github.com/shareed2k/honey/internal/searchrun"
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
	ch      <-chan engine.HostExecResult
	err     error
	lastReq engine.RunRequest // captured so tests can assert what the handler forwarded
}

func (f *fakeRunner) DryRun(_ context.Context, req engine.RunRequest) (string, error) {
	f.lastReq = req
	return "dry-run plan", nil
}

func (f *fakeRunner) Execute(_ context.Context, req engine.RunRequest) (<-chan engine.HostExecResult, error) {
	f.lastReq = req
	return f.ch, f.err
}

func (f *fakeRunner) ExecuteAndWait(_ context.Context, req engine.RunRequest) error {
	f.lastReq = req
	return nil
}

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

// recipe_run admission auditing lives in the RecipeRunner (see engine
// TestAdmitRecipe_audits_*). At the handler layer we assert the complementary
// facts: the handler forwards the correct Source into the RunRequest and does
// NOT emit the recipe_run event itself (which would double-audit).
func TestHandleCueExec_forwardsSourceWeb(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	s := newTestServer(t, Options{AuditSink: sink})
	fr := &fakeRunner{ch: closedResultChan()}
	s.recipesAPI.runner = fr

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(cueExecBody("myrecipe", true)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if n := len(sink.all()); n != 0 {
		t.Fatalf("handler must not emit recipe_run audit (runner does); got %d (status=%d body=%s)", n, w.Code, w.Body)
	}
	if fr.lastReq.Source != "web" {
		t.Errorf("forwarded Source = %q, want %q", fr.lastReq.Source, "web")
	}
}

func TestHandleCueExec_pendingApprovalReturns202(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	s := newTestServer(t, Options{AuditSink: sink})
	fr := &fakeRunner{err: &engine.ErrPendingApproval{ID: "appr-abc", Reason: "risky op"}}
	s.recipesAPI.runner = fr

	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(cueExecBody("deploy.cue", true)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 on pending approval, got %d body=%s", w.Code, w.Body)
	}
	if n := len(sink.all()); n != 0 {
		t.Fatalf("handler must not emit recipe_run audit; got %d", n)
	}
	if fr.lastReq.Source != "web" {
		t.Errorf("forwarded Source = %q, want %q", fr.lastReq.Source, "web")
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

// newWebhookTestServer creates a server with a temp recipe file, a dummy search
// registry, and the given AuditSink, then replaces the runner with fakeRunner.
func newWebhookTestServer(t *testing.T, recipeCUE, appTarget string, sink *captureSink, fr *fakeRunner) *Server {
	t.Helper()
	dir := t.TempDir()
	recipePath := filepath.Join(dir, "recipe.cue")
	if err := os.WriteFile(recipePath, []byte(recipeCUE), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "honey.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"myapp": {
				Type:         apps.AppTypeRecipe,
				TargetRecipe: recipePath,
				Target:       appTarget,
			},
		},
	}
	q, _ := queue.NewAntsQueue(5)
	s := newTestServer(t, Options{
		ConfigPath:     configPath,
		Config:         cfg,
		AuditSink:      sink,
		SearchRegistry: searchrun.NewRegistry([]searchrun.ProviderFactory{dummyFactory{}}),
	})
	s.webhookQueue = q
	s.recipesAPI.runner = fr
	return s
}

const webhookSyncCUE = `
recipe: {
	name: "hook-recipe"
	webhooks: {
		"deploy": {async: false}
	}
	steps: [{host: "*", command: "echo hi"}]
}
`

const webhookAsyncCUE = `
recipe: {
	name: "hook-recipe"
	webhooks: {
		"deploy": {async: true}
	}
	steps: [{host: "*", command: "echo hi"}]
}
`

// Sync webhook runs the recipe inline via runner.Execute, so we can assert the
// handler forwarded Source="webhook" (the recipe_run audit itself is verified in
// the engine tests). The handler must not emit the event itself.
func TestHandleRecipeWebhook_forwardsSourceWebhook_sync(t *testing.T) {
	sink := &captureSink{}
	fr := &fakeRunner{ch: closedResultChan()}
	s := newWebhookTestServer(t, webhookSyncCUE, "dynamic:localhost", sink, fr)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/myapp/deploy", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if n := len(sink.all()); n != 0 {
		t.Fatalf("handler must not emit recipe_run audit (runner does); got %d (status=%d body=%s)", n, w.Code, w.Body)
	}
	if fr.lastReq.Source != "webhook" {
		t.Errorf("forwarded Source = %q, want %q", fr.lastReq.Source, "webhook")
	}
}

// Async webhook enqueues the run and returns 202 immediately; the run (and its
// recipe_run audit) happens later in the queue goroutine, so we only assert the
// synchronous handler contract here.
func TestHandleRecipeWebhook_asyncReturns202(t *testing.T) {
	sink := &captureSink{}
	s := newWebhookTestServer(t, webhookAsyncCUE, "dynamic:localhost", sink, &fakeRunner{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/myapp/deploy", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", w.Code, w.Body)
	}
}

func TestHandleRecipesStoreSave_emitsAuditEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink := &captureSink{}
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir

	s := newTestServer(t, Options{AuditSink: sink, Config: cfg})

	body := `{"content": "package honey\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/store/myrecipe.cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	e := events[0]
	if e.Action != "recipe_save" {
		t.Errorf("Action = %q, want %q", e.Action, "recipe_save")
	}
	if e.Target != "myrecipe.cue" {
		t.Errorf("Target = %q, want %q", e.Target, "myrecipe.cue")
	}
	if e.Source != "web" {
		t.Errorf("Source = %q, want %q", e.Source, "web")
	}
}

func TestHandleRecipesStoreDelete_emitsAuditEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sink := &captureSink{}
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir

	// Pre-create the file so Delete succeeds.
	if err := os.WriteFile(filepath.Join(dir, "myrecipe.cue"), []byte("package honey\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t, Options{AuditSink: sink, Config: cfg})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/recipes/store/myrecipe.cue", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(events))
	}
	e := events[0]
	if e.Action != "recipe_delete" {
		t.Errorf("Action = %q, want %q", e.Action, "recipe_delete")
	}
	if e.Target != "myrecipe.cue" {
		t.Errorf("Target = %q, want %q", e.Target, "myrecipe.cue")
	}
}

func TestHandleCueExec_forwardsApprovalID(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	s := newTestServer(t, Options{AuditSink: sink})
	fr := &fakeRunner{ch: closedResultChan()}
	s.recipesAPI.runner = fr

	body := `{"recipe_content":{"name":"deploy.cue","steps":[{"host":"*","command":"echo hi"}]},` +
		`"execute":true,"ssh_user":"ops","records":[{"provider":"static","name":"h1","primary_ip":"1.1.1.1"}],` +
		`"approval_id":"appr-xyz"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if fr.lastReq.ApprovalID != "appr-xyz" {
		t.Errorf("forwarded ApprovalID = %q, want %q", fr.lastReq.ApprovalID, "appr-xyz")
	}
	if fr.lastReq.Source != "web" {
		t.Errorf("forwarded Source = %q, want %q", fr.lastReq.Source, "web")
	}
}
