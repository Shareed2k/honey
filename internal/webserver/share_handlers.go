package webserver

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/jit"
)

// isLiveTerminalGrant reports whether g is a live-terminal share grant (see
// applyLiveTerminalShare / jitKindLiveTerminal).
func isLiveTerminalGrant(g jit.Grant) bool {
	return g.Resource.Meta["kind"] == jitKindLiveTerminal
}

// shareSessionView is the wire shape of one live-terminal share in the
// operator-facing "Live shares" panel: grant metadata plus its LIVE
// attachment state (attached_guests/session_alive), which is computed fresh
// on every list call by querying tmux — it is never persisted on the grant.
type shareSessionView struct {
	GrantID        string         `json:"grant_id"`
	Capability     jit.Capability `json:"capability"`
	MuxSession     string         `json:"mux_session"`
	Actor          string         `json:"actor"`
	CreatedAt      time.Time      `json:"created_at"`
	ExpiresAt      time.Time      `json:"expires_at,omitempty"`
	Redemptions    int            `json:"redemptions"`
	MaxRedemptions int            `json:"max_redemptions"`
	AttachedGuests int            `json:"attached_guests"`
	SessionAlive   bool           `json:"session_alive"`
}

// newShareSessionView builds a view of g, querying tmux for its live
// attachment state.
func newShareSessionView(g jit.Grant) shareSessionView {
	mux := g.Resource.Meta["mux_session"]
	guests, alive := shareAttachedGuests(mux)
	var capability jit.Capability
	switch {
	case hasCapability(g.Capabilities, jit.CapCollab):
		capability = jit.CapCollab
	case hasCapability(g.Capabilities, jit.CapWatch):
		capability = jit.CapWatch
	}
	return shareSessionView{
		GrantID:        g.ID,
		Capability:     capability,
		MuxSession:     mux,
		Actor:          g.Actor,
		CreatedAt:      g.CreatedAt,
		ExpiresAt:      g.ExpiresAt,
		Redemptions:    g.Redemptions,
		MaxRedemptions: g.MaxRedemptions,
		AttachedGuests: guests,
		SessionAlive:   alive,
	}
}

// shareAttachedGuests counts the READ-ONLY tmux clients attached to mux — a
// guest of this live-terminal share. After the earlier security fix every
// guest attaches `tmux attach -r`, while the operator's own client never
// does, so #{client_readonly} reliably discriminates a guest from the
// operator (see ptyMuxTmuxGuestAttach). mux is re-validated here regardless
// of whether the caller already did, so the "validated before argv" /
// "#nosec G204" invariant holds independent of the call site; an invalid name
// is reported as a dead session, never passed to tmux. Every tmux call goes
// through tmuxRunGuest, the package's BOUNDED (2s) runner, since this is
// reachable from a plain polling GET — a wedged tmux server must not be able
// to hang the request. A tmux error (no server, session gone) is reported as
// session_alive:false, attached_guests:0, never a 500.
func shareAttachedGuests(mux string) (guests int, alive bool) {
	if !validHoneyMuxSessionName(mux) && !validInterceptMuxName(mux) {
		return 0, false
	}
	out, err := tmuxRunGuest("list-clients", "-t", mux, "-F", "#{client_readonly}")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "1" {
			guests++
		}
	}
	return guests, true
}

// detachShareGuests detaches every READ-ONLY tmux client of mux — the guests
// of this live-terminal share — by client tty, and returns how many were
// detached. It NEVER issues `kill-session` and NEVER `detach-client -t <mux>`
// (the session/target form, which would also drop the operator's own
// non-read-only client): only `detach-client -t <client_tty>` for a tty
// tmux itself reported read-only. mux is re-validated here independent of
// the caller, same invariant as shareAttachedGuests, and every call goes
// through the bounded tmuxRunGuest runner. A tmux error (no server, session
// gone) or a session with no clients simply detaches nothing.
func detachShareGuests(mux string) int {
	if !validHoneyMuxSessionName(mux) && !validInterceptMuxName(mux) {
		return 0
	}
	out, err := tmuxRunGuest("list-clients", "-t", mux, "-F", "#{client_tty} #{client_readonly}")
	if err != nil {
		return 0
	}
	detached := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != "1" {
			continue
		}
		tty := fields[0]
		if _, err := tmuxRunGuest("detach-client", "-t", tty); err != nil {
			zap.L().Debug("share: detach-client failed for guest tty", zap.String("tty", tty), zap.Error(err))
			continue
		}
		detached++
	}
	return detached
}

// handleListShareSessions lists the live-terminal shares that are currently
// live-shareable: any live_terminal grant that has not reached a terminal
// status (denied/revoked). Approved-but-expired grants are deliberately kept
// in this list rather than filtered by the store's Active() predicate — the
// entire point of this panel is to surface a guest who is STILL attached
// after the grant's window or single-use redemption is spent (the default
// live-terminal share is single-use), so an operator can see and kill it;
// Active() would hide exactly that case. expires_at is still returned for
// display, and session_alive/attached_guests (queried live from tmux) convey
// the actual truth regardless of the grant's own window.
// @Summary List live-terminal shares
// @Tags jit
// @Produce json
// @Success 200 {object} map[string]any
// @Router /api/v1/share/sessions [get]
// @Security BearerAuth
func (s *Server) handleListShareSessions(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	all := s.opts.Jit.List() // already newest-first, stable
	shares := make([]jit.Grant, 0, len(all))
	for _, g := range all {
		if isLiveTerminalGrant(g) && g.Status == jit.StatusApproved {
			shares = append(shares, g)
		}
	}
	page, perPage := paginateParams(r)
	paged, total := paginateSlice(shares, page, perPage)

	views := make([]shareSessionView, 0, len(paged))
	for _, g := range paged {
		views = append(views, newShareSessionView(g))
	}
	writeJSON(w, map[string]any{"sessions": views, "total": total, "page": page, "per_page": perPage})
}

// shareKillResponse is the POST .../kill response: how many guest clients
// were just detached, and how many remain attached afterward (normally 0).
type shareKillResponse struct {
	GrantID        string `json:"grant_id"`
	Detached       int    `json:"detached"`
	AttachedGuests int    `json:"attached_guests"`
}

// handleKillShareSession cuts the guests off a live-terminal share WITHOUT
// touching the operator's own session: it revokes the grant (so the code can
// never be redeemed again) and detaches only the READ-ONLY tmux clients of
// its mux_session (the guests) — never kill-session, never detach-client
// against the session itself. Idempotent: killing an already-revoked or
// already-dead share is a 200 with attached_guests:0, not an error, so a
// retried or double-clicked kill never surprises the operator.
// @Summary Kill a live-terminal share (revoke + detach guests)
// @Tags jit
// @Produce json
// @Param grant_id path string true "grant id"
// @Success 200 {object} shareKillResponse
// @Failure 404 {object} map[string]string
// @Router /api/v1/share/sessions/{grant_id}/kill [post]
// @Security BearerAuth
func (s *Server) handleKillShareSession(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	id := chi.URLParam(r, "grant_id")
	actor := actorFromCtx(r.Context())

	g, ok := s.opts.Jit.Get(id)
	if !ok {
		httpError(w, fmt.Errorf("grant %q not found", id), http.StatusNotFound)
		return
	}
	if !isLiveTerminalGrant(g) {
		httpError(w, fmt.Errorf("grant %q is not a live-terminal share", id), http.StatusBadRequest)
		return
	}
	mux := g.Resource.Meta["mux_session"]

	// Revoke so the code can never be redeemed again. An already-terminal
	// grant (already revoked/denied/expired) is exactly the idempotent retry
	// case, not a failure — proceed to the (best-effort) detach below either
	// way.
	if _, err := s.opts.Jit.Revoke(id, actor); err != nil {
		switch {
		case errors.Is(err, jit.ErrGrantNotActive):
			// already terminal — idempotent, fall through to detach.
		case errors.Is(err, jit.ErrGrantNotFound):
			httpError(w, err, http.StatusNotFound)
			return
		default:
			httpError(w, err, http.StatusInternalServerError)
			return
		}
	}

	detached := detachShareGuests(mux)
	remaining, _ := shareAttachedGuests(mux)

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "share_session_killed",
		Target:     mux,
		Decision:   "deny",
		ApprovalID: id,
		Extra:      map[string]string{"detached_guests": strconv.Itoa(detached)},
	})

	writeJSON(w, shareKillResponse{GrantID: id, Detached: detached, AttachedGuests: remaining})
}
