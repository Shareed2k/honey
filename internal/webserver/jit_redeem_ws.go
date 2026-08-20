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
	"github.com/shareed2k/honey/internal/jit"
)

// handleJITRedeemTerminal is the browser web-terminal redeem for a share link:
// a recipient with only the link (NO honey login) opens a live interactive
// terminal to the granted record. It is a thin adapter over the existing
// web-terminal pipeline — after authorizing the link a shell grant reuses
// serveWebInteractive verbatim, so docker/k8s/ssh and mesh-routed records all
// dispatch through the one InteractiveStreamer seam. A live_terminal grant
// (Meta["kind"]=="live_terminal") instead ATTACHES to the operator's EXISTING
// tmux-backed session via handleLiveTerminalAttach — watch (read-only) or
// collaborate (read-write) — never opening a brand-new shell. Either way the
// guest reaches the session ONLY through this redeemed, code-authenticated
// grant; there is no path here (or anywhere) that lets a plain-token client
// attach to someone else's session_id.
//
// Every check that needs no WebSocket frame runs BEFORE the upgrade and returns
// a plain HTTP status; everything after the upgrade is reported over the socket
// (as a text frame) and then the handler returns so the deferred Close runs.
// Pre-upgrade lookup failures collapse to a single generic 404 so a probe cannot
// distinguish unknown from expired from wrong-delivery from bad-mux_session
// codes; the record (and, for a live grant, the mux_session) is reconstructed
// from the grant (never the client), so a recipient cannot substitute another
// host or session.
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

	// A live_terminal grant attaches instead of opening a new shell. Everything
	// this branch needs is decidable from the grant alone, so it runs here,
	// pre-upgrade: an absent/invalid mux_session, or a capability that is
	// neither watch nor collaborate, collapses to the same generic 404 as any
	// other bad code — never a distinguishable error a probe could use.
	isLive := g.Resource.Meta["kind"] == "live_terminal"
	// FIX-6: a watch/collaborate capability is only ever meaningful on a
	// live_terminal grant (Store.load() does not re-run validateGrant, so a
	// persisted grant whose meta lost "kind" would otherwise fall through
	// below and redeem as a brand-new, full read-write shell via
	// serveWebInteractive — a read-only or attach-only capability failing
	// OPEN into an interactive shell). Collapse to the same generic 404 as
	// any other bad code.
	if grantCapabilityMismatchesKind(g, isLive) {
		httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
		return
	}
	muxSession := g.Resource.Meta["mux_session"]
	var liveCapability jit.Capability
	if isLive {
		if !validHoneyMuxSessionName(muxSession) && !validInterceptMuxName(muxSession) {
			httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
			return
		}
		// NEW-3: re-resolve to tmux's canonical session name at redeem time
		// too, same "re-validate independent of grant-create time" pattern as
		// the name-format check above (applyLiveTerminalShare already does
		// this at grant-create, so this is normally a no-op exact match; it
		// is what catches a grant stored before this fix, or a session
		// renamed since). Everything from here on — the attach, the OPA
		// input, and the audit record — uses ONLY this canonical value, never
		// whatever the grant happened to carry. A non-exact/prefix match
		// collapses into the same generic 404 as any other bad code.
		canonicalMux, cerr := tmuxCanonicalSessionName(muxSession)
		if cerr != nil {
			httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
			return
		}
		muxSession = canonicalMux
		switch {
		case hasCapability(g.Capabilities, jit.CapCollab):
			liveCapability = jit.CapCollab
		case hasCapability(g.Capabilities, jit.CapWatch):
			liveCapability = jit.CapWatch
		default:
			httpError(w, fmt.Errorf("invalid or expired link"), http.StatusNotFound)
			return
		}
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	// NEW-11: bound every frame from this share-code holder BEFORE the very
	// first read (the hello frame, right below) — covers both branches
	// (live-terminal attach and the plain shell grant via
	// serveWebInteractive), unlike round 2's guestReadLimitBytes call inside
	// handleLiveTerminalAttach, which ran after an unbounded hello read and
	// never ran at all for a shell grant.
	conn.SetReadLimit(guestReadLimitBytes)
	defer func() {
		if rec := recover(); rec != nil {
			zap.L().Error("web terminal panic", zap.Any("recover", rec))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"internal server error"}`))
		}
		_ = conn.Close()
	}()

	// One hello frame: only Cols/Rows are honored. hello.Record and hello.SSHUser
	// are deliberately ignored for authorization — the target (and, for a live
	// grant, the session) are fixed by the grant, so a client cannot redirect
	// the session.
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

	// actor is the audit/authorization identity for the link; user is the login
	// on the target host (unused for a live-terminal attach, which has no login
	// of its own — it joins the operator's already-running shell).
	actor := firstNonEmpty(g.Recipient, "share:"+g.ID)
	user := firstNonEmpty(g.Recipient, rec.Meta["ssh_user"])
	if user == "" {
		user = s.sshUser("")
	}

	if err := s.evalInteractiveSession(r.Context(), actor, rec, string(liveCapability), muxSession); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("session denied: "+err.Error()))
		return
	}

	// Consume the redemption only now, after the OPA gate. This still races a
	// concurrent redeem hitting the cap or the grant expiring in between; that
	// race collapses to the same generic error as everything else.
	if _, err := s.opts.Jit.Redeem(code); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid or expired link"}`))
		return
	}

	// A shared external session is always recorded when a record dir is
	// configured (force record=true), independent of any client-supplied flag.
	recorder := newWebSessionRecorder(s.opts.RecordDir, true, rec, user)
	if recorder != nil {
		recorder.RecordResize(cols, rows)
		defer recorder.Close()
	}

	auditExtra := map[string]string{"delivery": "web"}
	if isLive {
		auditExtra["capability"] = string(liveCapability)
		// MED-3: record which live session was joined, not just that a live
		// share happened — the audit trail otherwise can't say which session.
		auditExtra["mux_session"] = muxSession
	}
	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "jit_redeemed",
		Target:     rec.Name,
		Decision:   "allow",
		ApprovalID: g.ID,
		Extra:      auditExtra,
	})

	// guard carries the per-command guard's risk+policy inputs. For a plain
	// shell grant (below) it behaves like any other operator web terminal —
	// Mode comes from web.guard_mode. For a live_terminal collaborate guest,
	// handleLiveTerminalAttach overrides Mode to always-enforce (untrusted
	// party); a watch guest ignores it entirely (no stdin to guard).
	guard := termGuardInputs{Enforcer: s.opts.Enforcer, Actor: actor, Record: rec, AuditSink: s.opts.AuditSink, Mode: s.webGuardMode()}

	if isLive {
		mode := attachReadonly
		if liveCapability == jit.CapCollab {
			mode = attachShared
		}
		if err := handleLiveTerminalAttach(conn, muxSession, mode, cols, rows, recorder, guard); err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		}
		return
	}

	// Reuse the existing pipeline as-is: serveWebInteractive already handles the
	// mesh-proxy case and the ssh/docker/k8s InteractiveStreamer (plus the
	// leaf-SSH fallback). This handler spawns no goroutines of its own — the two
	// terminal goroutines are owned by handleWebInteractiveStreams and exit on
	// conn close / stdin EOF, so returning here lets the deferred Close run.
	ex := s.opts.ExecRegistry.ForRecord(rec)
	serveWebInteractive(conn, ex, user, rec, cols, rows, recorder, guard)
}

// grantCapabilityMismatchesKind reports whether g carries a watch/collaborate
// capability without being a live_terminal grant (FIX-6). Grant.Create's own
// validateGrant rejects this combination at creation time, but Store.load()
// does not re-run it on a persisted grant — so a grant whose meta lost "kind"
// (corruption, or one written before this feature existed) would otherwise
// fall through to a brand-new, full read-write shell via serveWebInteractive:
// a read-only or attach-only capability failing OPEN into an interactive
// shell. This must fail CLOSED instead.
func grantCapabilityMismatchesKind(g jit.Grant, isLive bool) bool {
	return !isLive && (hasCapability(g.Capabilities, jit.CapWatch) || hasCapability(g.Capabilities, jit.CapCollab))
}
