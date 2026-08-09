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

// handleJITRedeemTerminal is the browser web-terminal redeem for a share link:
// a recipient with only the link (NO honey login) opens a live interactive
// terminal to the granted record. It is a thin adapter over the existing
// web-terminal pipeline — after authorizing the link it reuses
// serveWebInteractive verbatim, so docker/k8s/ssh and mesh-routed records all
// dispatch through the one InteractiveStreamer seam.
//
// Every check that needs no WebSocket frame runs BEFORE the upgrade and returns
// a plain HTTP status; everything after the upgrade is reported over the socket
// (as a text frame) and then the handler returns so the deferred Close runs.
// Pre-upgrade lookup failures collapse to a single generic 404 so a probe cannot
// distinguish unknown from expired from wrong-delivery codes; the record is
// reconstructed from the grant (never the client), so a recipient cannot
// substitute another host.
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
	defer func() {
		if rec := recover(); rec != nil {
			zap.L().Error("web terminal panic", zap.Any("recover", rec))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"internal server error"}`))
		}
		_ = conn.Close()
	}()

	// One hello frame: only Cols/Rows are honored. hello.Record and hello.SSHUser
	// are deliberately ignored for authorization — the target and login are fixed
	// by the grant, so a client cannot redirect the session.
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
	// on the target host.
	actor := firstNonEmpty(g.Recipient, "share:"+g.ID)
	user := firstNonEmpty(g.Recipient, rec.Meta["ssh_user"])
	if user == "" {
		user = s.sshUser("")
	}

	if err := s.evalInteractiveSession(r.Context(), actor, rec); err != nil {
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

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "jit_redeemed",
		Target:     rec.Name,
		Decision:   "allow",
		ApprovalID: g.ID,
		Extra:      map[string]string{"delivery": "web"},
	})

	// Reuse the existing pipeline as-is: serveWebInteractive already handles the
	// mesh-proxy case and the ssh/docker/k8s InteractiveStreamer (plus the
	// leaf-SSH fallback). This handler spawns no goroutines of its own — the two
	// terminal goroutines are owned by handleWebInteractiveStreams and exit on
	// conn close / stdin EOF, so returning here lets the deferred Close run.
	ex := s.opts.ExecRegistry.ForRecord(rec)
	serveWebInteractive(conn, ex, user, rec, cols, rows, recorder)
}
