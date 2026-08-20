package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/hosts"
)

// handleJITRedeemTerminal is the browser web-terminal redeem for a share
// link: a recipient with only the link (NO honey login) opens a live
// interactive terminal to the granted record — their OWN working session, not
// a view into anyone else's. When this host has tmux on PATH (shareMuxAvailable),
// the shell runs inside a tmux session named deterministically from the
// grant (shareGuestMuxName), so the operator can later watch it read-only and
// kill it from the Access Requests panel (see share_handlers.go /
// share_watch.go); the guest itself is the ordinary, exclusive, read-write
// client of that session (handleShareGuestPtyProxy), never anything
// restricted. With no multiplexer available, this falls back to a plain,
// unobservable shell via serveWebInteractive rather than failing the guest's
// access. Either way the guest reaches the session ONLY through this
// redeemed, code-authenticated grant; there is no path here (or anywhere)
// that lets a plain-token client attach to someone else's session_id.
//
// Every check that needs no WebSocket frame runs BEFORE the upgrade and returns
// a plain HTTP status; everything after the upgrade is reported over the socket
// (as a text frame) and then the handler returns so the deferred Close runs.
// Pre-upgrade lookup failures collapse to a single generic 404 so a probe
// cannot distinguish unknown from expired from wrong-delivery codes; the
// record is reconstructed from the grant (never the client), so a recipient
// cannot substitute another host.
//
// The guest's session MUST be recorded: newWebSessionRecorder silently
// returns nil when RecordDir is empty, which would otherwise hand out an
// unrecorded, unreplayable guest session — that fails closed here, refusing
// the redeem instead.
//
// Scope: web-terminal share links cover ssh/docker/k8s and mesh-routed records.
// They do NOT currently support Proxmox-serial or TrueNAS-console records (those
// are cert-less console targets handled elsewhere in the web-terminal dispatch).
// Unlike the gateway, the web-terminal pipeline records the session and OPA-gates
// it (action "interactive_session") but does not mask terminal output.
func (s *Server) handleJITRedeemTerminal(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	if s.opts.ExecRegistry == nil {
		httpError(w, fmt.Errorf("exec registry not available"), http.StatusServiceUnavailable)
		return
	}

	code := chi.URLParam(r, "code")
	g, err := s.opts.Jit.Peek(code)
	if err != nil {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}
	if !jitOffersWeb(g) || !s.opts.Jit.Active(g.ID) {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// NEW-11: bound every frame from this share-code holder BEFORE the very
	// first read (the hello frame, right below).
	conn.SetReadLimit(guestReadLimitBytes)
	defer func() {
		if rec := recover(); rec != nil {
			zap.L().Error("web terminal panic", zap.Any("recover", rec))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"internal server error"}`))
		}
		_ = conn.Close()
	}()

	// One hello frame: only Cols/Rows are honored. hello.Record and
	// hello.SSHUser are deliberately ignored for authorization — the target is
	// fixed by the grant, so a client cannot redirect the session.
	_, helloRaw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello WSHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}
	cols, rows := hello.Cols, hello.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}

	// Reconstruct the target from the grant, never the client.
	rec := hosts.Record{
		Name:      g.Resource.Name,
		Provider:  g.Resource.Provider,
		PrimaryIP: g.Resource.PrimaryIP,
		Meta:      g.Resource.Meta,
	}

	actor := firstNonEmpty(g.Recipient, "share:"+g.ID)
	user := firstNonEmpty(g.Recipient, rec.Meta["ssh_user"])
	if user == "" {
		user = s.sshUser("")
	}

	if err := s.evalInteractiveSession(r.Context(), actor, rec); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("session denied: "+err.Error()))
		return
	}

	// A guest's session must always be recorded and replayable — fail closed
	// rather than silently run an unrecorded session (RecordDir empty, or the
	// recorder otherwise couldn't be created). Checked BEFORE consuming the
	// redemption below: a misconfigured server (no RecordDir) must not burn a
	// single-use share link on every attempt.
	recorder := newWebSessionRecorder(s.opts.RecordDir, true, rec, user)
	if recorder == nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"recording is required for access-request sessions but could not be started"}`))
		return
	}
	defer recorder.Close()

	// Consume the redemption only now, after every other precondition has
	// passed. This still races a concurrent redeem hitting the cap or the
	// grant expiring in between; that race collapses to the same generic
	// error as everything else.
	if _, err := s.opts.Jit.Redeem(code); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid or expired link"}`))
		return
	}
	recorder.RecordResize(cols, rows)

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "jit_redeemed",
		Target:     rec.Name,
		Decision:   "allow",
		ApprovalID: g.ID,
		Extra:      map[string]string{"delivery": "web"},
	})

	// guard carries the per-command guard's risk+policy inputs for the
	// guest's own terminal — behaves like any other web terminal, gated by
	// web.guard_mode.
	guard := termGuardInputs{Enforcer: s.opts.Enforcer, Actor: actor, Record: rec, AuditSink: s.opts.AuditSink, Mode: s.webGuardMode()}

	if shareMuxAvailable() {
		muxHello := WSHello{SessionID: shareGuestSessionID(g.ID), SSHUser: user, Record: rec, Cols: cols, Rows: rows}
		muxName := shareGuestMuxName(g.ID)
		err := handleShareGuestPtyProxy(conn, muxHello, recorder, s.opts.ConfigPath, guard, muxName)
		if err == nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true}`))
			return
		}
		// If tmux disappeared between the shareMuxAvailable() check and here,
		// fall back to the plain shell below rather than failing the guest's
		// access; any other error is fatal and reported.
		if err.Error() != "neither zellij nor tmux found on the server" {
			zap.L().Error("jit redeem: share mux proxy failed", zap.Error(err))
			recorder.RecordError(err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
			return
		}
	}

	// No multiplexer on this host: fall back to today's plain shell — not
	// observable by the operator, but the guest's access must not fail
	// because of it. Reuse the existing pipeline as-is: serveWebInteractive
	// already handles the mesh-proxy case and the ssh/docker/k8s
	// InteractiveStreamer (plus the leaf-SSH fallback). This handler spawns no
	// goroutines of its own — the two terminal goroutines are owned by
	// handleWebInteractiveStreams and exit on conn close / stdin EOF, so
	// returning here lets the deferred Close run.
	ex := s.opts.ExecRegistry.ForRecord(rec)
	serveWebInteractive(conn, ex, user, rec, cols, rows, recorder, guard)
}
