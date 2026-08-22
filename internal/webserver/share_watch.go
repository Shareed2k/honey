package webserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/engine"
)

// handleShareWatch is the OPERATOR-only, authed, read-only live view onto a
// guest's access-request session (Part 2 of the share/watch feature): `GET
// /ws/share/watch?grant=<id>`. It attaches with `tmux attach -r` — no stdin
// wired in at all, resize frames ignored (ptyProxyStdinPolicy{DropStdin:
// true, IgnoreResize: true}, the SAME mechanism a plain disconnect-safe
// bridge always used) — so an observer can never type into, resize, or
// otherwise influence the guest's session. Disconnecting reaps only the
// observer's own read-only tmux client (ptyProxyTeardown's guestPath=true):
// it NEVER kills or detaches the guest, on any teardown path (natural exit or
// the explicit × close_tab).
//
// Authed like /ws/ssh and /ws/intercept (its own s.authorized(r) check, not
// the session-code auth a redeemed share link uses) — this is an OPERATOR
// tool, never reachable with just a share code. Only a grant that actually
// offers a browser-terminal shell (jitOffersWeb) may be watched; anything
// else (a cert-only or non-web grant) is refused before the upgrade.
func (s *Server) handleShareWatch(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("grant"))
	g, ok := s.opts.Jit.Get(id)
	if !ok || !jitOffersWeb(g) {
		httpError(w, fmt.Errorf("grant %q not found", id), http.StatusNotFound)
		return
	}
	mux := shareGuestMuxName(g.ID)

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			zap.L().Error("share watch panic", zap.Any("recover", rec))
		}
		_ = conn.Close()
	}()

	// Drain (and ignore) the hello frame every terminal WS client sends, so
	// an operator's xterm.js needs no special-casing to talk to this route —
	// nothing in it is honored: this is a read-only view of whatever size the
	// guest's own session already is.
	//
	// Bounded, because a viewer that sends no hello at all must still get a
	// session rather than hang here forever: an unbounded read once turned a
	// client-side mount bug into an empty modal with no attach, no output and no
	// error. A missed hello costs nothing — its contents are ignored anyway.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, _ = conn.ReadMessage()
	_ = conn.SetReadDeadline(time.Time{})

	cmd, err := ptyMuxTmuxWatchAttach(mux)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	actor := userFromRequest(r, s.opts.TrustedProxyNets, s.opts.JWTPubKey)
	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{Source: "web", Actor: actor, Action: "share_watch_started", Target: mux, Decision: "allow", ApprovalID: g.ID})

	closeTabKill := make(chan struct{}, 1)
	hello := WSHello{}
	// guestPath=true on teardown below (LOW-6's invariant, reused here for
	// the symmetric case): an observer's own reap must NEVER kill-session the
	// guest's session — not on the explicit close_tab (×) branch, and not on
	// a natural ptyExited either.
	ptyExited := ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, mux, closeTabKill,
		ptyProxyStdinPolicy{DropStdin: true, IgnoreResize: true})
	ptyProxyTeardown(ptmx, cmd, mux, false, closeTabKill, ptyExited, func() {}, true)

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{Source: "web", Actor: actor, Action: "share_watch_stopped", Target: mux, Decision: "allow", ApprovalID: g.ID})
}
