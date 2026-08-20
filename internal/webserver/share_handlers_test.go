package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/jit"
)

// fakeTmuxClient is one simulated tmux client attached to a session.
type fakeTmuxClient struct {
	tty      string
	readonly bool
}

// fakeShareTmux simulates the verbs share_handlers.go issues against a
// single session (list-clients, kill-session) and records every argv it was
// called with, so a test can assert both the resulting behavior and the
// exact shape of every issued command. Safe for sequential use only,
// matching every other fake-tmux seam in this package.
type fakeShareTmux struct {
	clients []fakeTmuxClient
	alive   bool
	calls   [][]string
}

func (f *fakeShareTmux) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) == 0 {
		return nil, fmt.Errorf("empty tmux call")
	}
	switch args[0] {
	case "list-clients":
		if !f.alive {
			return nil, fmt.Errorf("can't find session")
		}
		var lines []string
		for _, c := range f.clients {
			ro := "0"
			if c.readonly {
				ro = "1"
			}
			lines = append(lines, ro)
		}
		return []byte(strings.Join(lines, "\n")), nil
	case "kill-session":
		if !f.alive {
			return nil, fmt.Errorf("can't find session")
		}
		f.alive = false
		f.clients = nil
		return nil, nil
	default:
		return nil, fmt.Errorf("fakeShareTmux: unexpected verb %q", args[0])
	}
}

// tmuxAbsent simulates "no tmux server" / "session gone" for every call.
func tmuxAbsent(_ ...string) ([]byte, error) {
	return nil, fmt.Errorf("no server running")
}

// withShareMuxAvailable overrides the shareMuxAvailable seam for the
// duration of the calling test, restoring the original on cleanup.
func withShareMuxAvailable(t *testing.T, available bool) {
	t.Helper()
	orig := shareMuxAvailable
	shareMuxAvailable = func() bool { return available }
	t.Cleanup(func() { shareMuxAvailable = orig })
}

func TestHandleListShareSessions_NilStore(t *testing.T) {
	s := &Server{opts: Options{AuditSink: audit.NewNoopSink()}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/share/sessions", nil)
	s.handleListShareSessions(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 body=%s", w.Code, w.Body)
	}
}

// TestHandleListShareSessions_FiltersToWebShellApprovedOnly proves the
// listing keeps an approved web/shell grant (the only kind that can ever
// redeem into a guest session), but excludes: a cert-only grant, and a
// web/shell grant that has been revoked.
func TestHandleListShareSessions_FiltersToWebShellApprovedOnly(t *testing.T) {
	defer swapTmuxRunGuest(tmuxAbsent)()
	withShareMuxAvailable(t, true)

	s, store := newJitTestServer(t, Options{})
	keep := createJITGrantDirect(t, store, "keepme")
	certOnly, _, err := store.Create(jit.Grant{
		Actor:        "alice",
		Resource:     jit.ResourceRef{Name: "cert-host", Provider: "ssh"},
		Capabilities: []jit.Capability{jit.CapExec},
		Delivery:     jit.DeliveryCert,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("create cert-only grant: %v", err)
	}
	_ = certOnly
	revoked := createJITGrantDirect(t, store, "gonebye")
	if _, err := store.Revoke(revoked.ID, "dave"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	w := doJSON(t, s, http.MethodGet, "/api/v1/share/sessions", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp struct {
		Sessions []shareSessionView `json:"sessions"`
		Total    int                `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 || len(resp.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want exactly the one approved web/shell grant", resp.Sessions)
	}
	if resp.Sessions[0].GrantID != keep.ID {
		t.Fatalf("grant_id = %q, want %q", resp.Sessions[0].GrantID, keep.ID)
	}
}

// TestHandleListShareSessions_ObserversAndSessionAlive covers both the happy
// path (tmux reachable, readonly clients counted as observers, the guest's
// own read-write client excluded) and the tmux-absent path (session_alive:
// false, observers:0, never a 500).
func TestHandleListShareSessions_ObserversAndSessionAlive(t *testing.T) {
	tests := []struct {
		name          string
		runner        func(...string) ([]byte, error)
		wantObservers int
		wantAlive     bool
	}{
		{
			name: "two observers, one guest client",
			runner: (&fakeShareTmux{alive: true, clients: []fakeTmuxClient{
				{tty: "/dev/pts/1", readonly: true},
				{tty: "/dev/pts/2", readonly: true},
				{tty: "/dev/pts/0", readonly: false},
			}}).run,
			wantObservers: 2,
			wantAlive:     true,
		},
		{
			name:          "no clients at all, session still alive",
			runner:        (&fakeShareTmux{alive: true}).run,
			wantObservers: 0,
			wantAlive:     true,
		},
		{
			name:          "tmux absent -> session_alive false, not a 500",
			runner:        tmuxAbsent,
			wantObservers: 0,
			wantAlive:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer swapTmuxRunGuest(tc.runner)()

			s, store := newJitTestServer(t, Options{})
			createJITGrantDirect(t, store, "livecheck")

			w := doJSON(t, s, http.MethodGet, "/api/v1/share/sessions", nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
			}
			var resp struct {
				Sessions []shareSessionView `json:"sessions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Sessions) != 1 {
				t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
			}
			got := resp.Sessions[0]
			if got.Observers != tc.wantObservers || got.SessionAlive != tc.wantAlive {
				t.Fatalf("observers/session_alive = %d/%v, want %d/%v", got.Observers, got.SessionAlive, tc.wantObservers, tc.wantAlive)
			}
		})
	}
}

// TestHandleListShareSessions_Observable proves the observable field tracks
// the host-wide shareMuxAvailable seam, independent of any one grant's own
// state.
func TestHandleListShareSessions_Observable(t *testing.T) {
	for _, want := range []bool{true, false} {
		t.Run(fmt.Sprintf("observable=%v", want), func(t *testing.T) {
			defer swapTmuxRunGuest(tmuxAbsent)()
			withShareMuxAvailable(t, want)

			s, store := newJitTestServer(t, Options{})
			createJITGrantDirect(t, store, "obscheck")

			w := doJSON(t, s, http.MethodGet, "/api/v1/share/sessions", nil)
			var resp struct {
				Sessions []shareSessionView `json:"sessions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(resp.Sessions) != 1 || resp.Sessions[0].Observable != want {
				t.Fatalf("sessions = %+v, want observable=%v", resp.Sessions, want)
			}
		})
	}
}

// TestHandleListShareSessions_Pagination mirrors the jit/grants pagination
// test against /share/sessions, confirming the same paginateParams/
// paginateSlice plumbing is wired through here too.
func TestHandleListShareSessions_Pagination(t *testing.T) {
	defer swapTmuxRunGuest(tmuxAbsent)()

	s, store := newJitTestServer(t, Options{})
	for i := 0; i < 3; i++ {
		createJITGrantDirect(t, store, fmt.Sprintf("page%d", i))
	}

	w := doJSON(t, s, http.MethodGet, "/api/v1/share/sessions?page=2&per_page=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp struct {
		Sessions []shareSessionView `json:"sessions"`
		Total    int                `json:"total"`
		Page     int                `json:"page"`
		PerPage  int                `json:"per_page"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 3 || resp.Page != 2 || resp.PerPage != 2 || len(resp.Sessions) != 1 {
		t.Fatalf("resp = %+v, want total=3 page=2 per_page=2 len(sessions)=1", resp)
	}
}

func TestHandleKillShareSession_NilStore(t *testing.T) {
	s := &Server{opts: Options{AuditSink: audit.NewNoopSink()}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/share/sessions/jit_x/kill", nil)
	s.handleKillShareSession(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 body=%s", w.Code, w.Body)
	}
}

func TestHandleKillShareSession_UnknownGrant(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/jit_nope/kill", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", w.Code, w.Body)
	}
}

func TestHandleKillShareSession_RefusesNonWebGrant(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	g, _, err := store.Create(jit.Grant{
		Actor:        "alice",
		Resource:     jit.ResourceRef{Name: "cert-host", Provider: "ssh"},
		Capabilities: []jit.Capability{jit.CapExec},
		Delivery:     jit.DeliveryCert,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("create cert-only grant: %v", err)
	}

	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+g.ID+"/kill", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body)
	}
	got, ok := store.Get(g.ID)
	if !ok || got.Status != jit.StatusApproved {
		t.Fatal("kill on a non-web grant must not revoke it")
	}
}

// TestHandleKillShareSession_RevokesAndKillsGuestSession is the load-bearing
// safety test: it proves (1) the grant is revoked, (2) the tmux argv issued
// is exactly `kill-session -t <the guest's own mux name>` — never anything
// else (no detach-client, no targeting a different session) — and (3) the
// response reports the session was alive and is now killed.
func TestHandleKillShareSession_RevokesAndKillsGuestSession(t *testing.T) {
	fake := &fakeShareTmux{alive: true, clients: []fakeTmuxClient{
		{tty: "/dev/pts/1", readonly: true},  // an observer
		{tty: "/dev/pts/0", readonly: false}, // the guest's own client
	}}
	defer swapTmuxRunGuest(fake.run)()

	s, store := newJitTestServer(t, Options{})
	g := createJITGrantDirect(t, store, "killme")
	wantMux := shareGuestMuxName(g.ID)

	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+g.ID+"/kill", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp shareKillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.SessionKilled {
		t.Fatalf("session_killed = %v, want true", resp.SessionKilled)
	}

	// (1) the grant itself is revoked.
	got, ok := store.Get(g.ID)
	if !ok || got.Status != jit.StatusRevoked {
		t.Fatalf("grant status = %v, want revoked", got.Status)
	}

	// (2) exactly one tmux call, kill-session against the guest's own mux name.
	if len(fake.calls) != 1 {
		t.Fatalf("tmux calls = %v, want exactly one kill-session call", fake.calls)
	}
	call := fake.calls[0]
	if call[0] != "kill-session" {
		t.Fatalf("call = %v, want kill-session", call)
	}
	if got := call[len(call)-1]; got != wantMux {
		t.Fatalf("kill-session target = %q, want the guest's own session %q", got, wantMux)
	}
}

// TestHandleKillShareSession_IdempotentOnAlreadyRevoked proves killing a
// share that was already revoked (e.g. a retried/double-clicked kill, or one
// revoked earlier via the plain jit "Revoke" action) is a 200 with
// session_killed:false, never an error.
func TestHandleKillShareSession_IdempotentOnAlreadyRevoked(t *testing.T) {
	defer swapTmuxRunGuest(tmuxAbsent)()

	s, store := newJitTestServer(t, Options{})
	g := createJITGrantDirect(t, store, "alreadydead")
	if _, err := store.Revoke(g.ID, "dave"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+g.ID+"/kill", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp shareKillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionKilled {
		t.Fatalf("resp = %+v, want a no-op (session_killed=false) kill on an already-dead share", resp)
	}
}

// TestHandleKillShareSession_TmuxAbsentStillRevokesAndReturns200 covers the
// "no live tmux server at all" idempotent case on a still-approved grant:
// the revoke must still happen even though nothing can be killed.
func TestHandleKillShareSession_TmuxAbsentStillRevokesAndReturns200(t *testing.T) {
	defer swapTmuxRunGuest(tmuxAbsent)()

	s, store := newJitTestServer(t, Options{})
	g := createJITGrantDirect(t, store, "notmux")

	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+g.ID+"/kill", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp shareKillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionKilled {
		t.Fatalf("resp = %+v, want session_killed=false when tmux is unreachable", resp)
	}
	got, ok := store.Get(g.ID)
	if !ok || got.Status != jit.StatusRevoked {
		t.Fatalf("grant status = %v, want revoked even when tmux is unreachable", got.Status)
	}
}
