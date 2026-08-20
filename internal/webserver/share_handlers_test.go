package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/jit"
)

// createLiveTerminalGrantDirect creates a live_terminal grant directly via
// the store (bypassing the HTTP create handler's tmux-liveness/ownership
// plumbing — already covered by TestHandleCreateJITGrant_LiveTerminalShare)
// so the sessions-list/kill tests can set up fixtures without a fake tmux
// server for grant creation itself.
func createLiveTerminalGrantDirect(t *testing.T, store *jit.Store, mux string, capability jit.Capability) jit.Grant {
	t.Helper()
	stored, _, err := store.Create(jit.Grant{
		Actor: "operator1",
		Resource: jit.ResourceRef{
			Name:     "op-terminal",
			Provider: "ssh",
			Meta:     map[string]string{"kind": jitKindLiveTerminal, "mux_session": mux},
		},
		Capabilities: []jit.Capability{capability},
		Delivery:     jit.DeliveryWeb,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("create live_terminal grant: %v", err)
	}
	return stored
}

// fakeTmuxClient is one simulated tmux client attached to a session.
type fakeTmuxClient struct {
	tty      string
	readonly bool
}

// fakeShareTmux simulates the two verbs share_handlers.go issues against a
// single session's client list (list-clients, detach-client) and records
// every argv it was called with, so a test can assert both the resulting
// behavior (who got detached) and the exact shape of every issued command
// (e.g. that kill-session never appears). detach-client actually removes the
// matching tty from the simulated client set, so a later list-clients call
// (the kill handler's post-detach recount) reflects the real drop. Safe for
// sequential use only, matching every other fake-tmux seam in this package.
type fakeShareTmux struct {
	mu      sync.Mutex
	clients []fakeTmuxClient
	calls   [][]string
}

func (f *fakeShareTmux) run(args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), args...))
	if len(args) == 0 {
		return nil, fmt.Errorf("empty tmux call")
	}
	switch args[0] {
	case "list-clients":
		format := args[len(args)-1]
		var lines []string
		for _, c := range f.clients {
			ro := "0"
			if c.readonly {
				ro = "1"
			}
			if format == "#{client_readonly}" {
				lines = append(lines, ro)
			} else {
				lines = append(lines, c.tty+" "+ro)
			}
		}
		return []byte(strings.Join(lines, "\n")), nil
	case "detach-client":
		target := args[len(args)-1]
		kept := f.clients[:0:0]
		for _, c := range f.clients {
			if c.tty != target {
				kept = append(kept, c)
			}
		}
		f.clients = kept
		return nil, nil
	default:
		return nil, fmt.Errorf("fakeShareTmux: unexpected verb %q", args[0])
	}
}

// tmuxAbsent simulates "no tmux server" / "session gone" for every call.
func tmuxAbsent(_ ...string) ([]byte, error) {
	return nil, fmt.Errorf("no server running")
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

// TestHandleListShareSessions_FiltersToLiveApprovedOnly proves the listing
// keeps a live_terminal grant that is Approved, but excludes: a plain
// (non-live_terminal) grant, and a live_terminal grant that has been revoked.
func TestHandleListShareSessions_FiltersToLiveApprovedOnly(t *testing.T) {
	defer swapTmuxRunGuest(tmuxAbsent)()

	s, store := newJitTestServer(t, Options{})
	live := createLiveTerminalGrantDirect(t, store, "honey_keepme", jit.CapWatch)
	createJITGrantDirect(t, store, "plain-host") // non-live_terminal, must be excluded
	revoked := createLiveTerminalGrantDirect(t, store, "honey_gonebye", jit.CapCollab)
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
		t.Fatalf("sessions = %+v, want exactly the one approved live_terminal grant", resp.Sessions)
	}
	if resp.Sessions[0].GrantID != live.ID {
		t.Fatalf("grant_id = %q, want %q", resp.Sessions[0].GrantID, live.ID)
	}
}

// TestHandleListShareSessions_AttachedGuestsAndSessionAlive covers both the
// happy path (tmux reachable, readonly clients counted, non-readonly ones
// excluded) and the tmux-absent path (session_alive:false, attached_guests:0,
// never a 500).
func TestHandleListShareSessions_AttachedGuestsAndSessionAlive(t *testing.T) {
	tests := []struct {
		name       string
		runner     func(...string) ([]byte, error)
		wantGuests int
		wantAlive  bool
	}{
		{
			name: "two readonly guests, one operator client",
			runner: (&fakeShareTmux{clients: []fakeTmuxClient{
				{tty: "/dev/pts/1", readonly: true},
				{tty: "/dev/pts/2", readonly: true},
				{tty: "/dev/pts/0", readonly: false},
			}}).run,
			wantGuests: 2,
			wantAlive:  true,
		},
		{
			name:       "no clients at all, session still alive",
			runner:     (&fakeShareTmux{}).run,
			wantGuests: 0,
			wantAlive:  true,
		},
		{
			name:       "tmux absent -> session_alive false, not a 500",
			runner:     tmuxAbsent,
			wantGuests: 0,
			wantAlive:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer swapTmuxRunGuest(tc.runner)()

			s, store := newJitTestServer(t, Options{})
			createLiveTerminalGrantDirect(t, store, "honey_livecheck", jit.CapWatch)

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
			if got.AttachedGuests != tc.wantGuests || got.SessionAlive != tc.wantAlive {
				t.Fatalf("attached_guests/session_alive = %d/%v, want %d/%v", got.AttachedGuests, got.SessionAlive, tc.wantGuests, tc.wantAlive)
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
		createLiveTerminalGrantDirect(t, store, fmt.Sprintf("honey_page%d", i), jit.CapWatch)
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

func TestHandleKillShareSession_RefusesNonLiveTerminalGrant(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	g := createJITGrantDirect(t, store, "plain-host")

	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+g.ID+"/kill", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body)
	}
	got, ok := store.Get(g.ID)
	if !ok || got.Status != jit.StatusApproved {
		t.Fatal("kill on a non-live_terminal grant must not revoke it")
	}
}

// TestHandleKillShareSession_RevokesAndDetachesOnlyGuests is the load-bearing
// safety test: it proves (1) the grant is revoked, (2) every read-only
// (guest) client is detached by tty, (3) the operator's non-read-only client
// is NEVER detached (its tty never appears in any detach-client call and it
// remains in the simulated client set afterward), and (4) no call in the
// entire sequence is ever "kill-session" — the one command that would also
// drop the operator.
func TestHandleKillShareSession_RevokesAndDetachesOnlyGuests(t *testing.T) {
	const mux = "honey_killme"
	fake := &fakeShareTmux{clients: []fakeTmuxClient{
		{tty: "/dev/pts/1", readonly: true},  // guest 1
		{tty: "/dev/pts/2", readonly: true},  // guest 2
		{tty: "/dev/pts/0", readonly: false}, // the OPERATOR — must survive
	}}
	defer swapTmuxRunGuest(fake.run)()

	s, store := newJitTestServer(t, Options{})
	g := createLiveTerminalGrantDirect(t, store, mux, jit.CapCollab)

	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+g.ID+"/kill", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp shareKillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Detached != 2 {
		t.Fatalf("detached = %d, want 2", resp.Detached)
	}
	if resp.AttachedGuests != 0 {
		t.Fatalf("attached_guests after kill = %d, want 0", resp.AttachedGuests)
	}

	// (1) the grant itself is revoked.
	got, ok := store.Get(g.ID)
	if !ok || got.Status != jit.StatusRevoked {
		t.Fatalf("grant status = %v, want revoked", got.Status)
	}

	// (2)+(3) the operator's client must still be attached; only the two
	// guest ttys were ever removed.
	fake.mu.Lock()
	remaining := append([]fakeTmuxClient(nil), fake.clients...)
	calls := append([][]string(nil), fake.calls...)
	fake.mu.Unlock()
	if len(remaining) != 1 || remaining[0].tty != "/dev/pts/0" {
		t.Fatalf("remaining simulated clients = %+v, want only the operator's /dev/pts/0", remaining)
	}

	// (4) no call anywhere in the sequence is kill-session, and no
	// detach-client call ever names the operator's tty or the session itself.
	for _, call := range calls {
		if call[0] == "kill-session" {
			t.Fatalf("kill-session must NEVER be issued by a share kill; calls=%v", calls)
		}
		if call[0] == "detach-client" {
			target := call[len(call)-1]
			if target == "/dev/pts/0" {
				t.Fatalf("detach-client must never target the operator's tty; calls=%v", calls)
			}
			if target == mux {
				t.Fatalf("detach-client must never target the session name (that would drop every client including the operator); calls=%v", calls)
			}
		}
	}
}

// TestHandleKillShareSession_IdempotentOnAlreadyRevoked proves killing a
// share that was already revoked (e.g. a retried/double-clicked kill, or one
// revoked earlier via the plain jit "Revoke" action) is a 200 with
// attached_guests:0, never an error.
func TestHandleKillShareSession_IdempotentOnAlreadyRevoked(t *testing.T) {
	defer swapTmuxRunGuest(tmuxAbsent)()

	s, store := newJitTestServer(t, Options{})
	g := createLiveTerminalGrantDirect(t, store, "honey_alreadydead", jit.CapWatch)
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
	if resp.AttachedGuests != 0 || resp.Detached != 0 {
		t.Fatalf("resp = %+v, want a no-op 0/0 kill on an already-dead share", resp)
	}
}

// TestHandleKillShareSession_TmuxAbsentStillRevokesAndReturns200 covers the
// "no live tmux server at all" idempotent case on a still-approved grant:
// the revoke must still happen even though nothing can be detached.
func TestHandleKillShareSession_TmuxAbsentStillRevokesAndReturns200(t *testing.T) {
	defer swapTmuxRunGuest(tmuxAbsent)()

	s, store := newJitTestServer(t, Options{})
	g := createLiveTerminalGrantDirect(t, store, "honey_notmux", jit.CapWatch)

	w := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+g.ID+"/kill", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp shareKillResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AttachedGuests != 0 || resp.Detached != 0 {
		t.Fatalf("resp = %+v, want 0/0 when tmux is unreachable", resp)
	}
	got, ok := store.Get(g.ID)
	if !ok || got.Status != jit.StatusRevoked {
		t.Fatalf("grant status = %v, want revoked even when tmux is unreachable", got.Status)
	}
}
