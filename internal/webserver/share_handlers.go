package webserver

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/jit"
)

// shareSessionView is the wire shape of one access-request guest session in
// the operator-facing panel: grant metadata plus its LIVE attachment state
// (session_alive/observers/observable), which is computed fresh on every
// list call by querying tmux — it is never persisted on the grant.
type shareSessionView struct {
	GrantID        string           `json:"grant_id"`
	Resource       jit.ResourceRef  `json:"resource"`
	Actor          string           `json:"actor"`
	Recipient      string           `json:"recipient,omitempty"`
	Capabilities   []jit.Capability `json:"capabilities"`
	CreatedAt      time.Time        `json:"created_at"`
	ExpiresAt      time.Time        `json:"expires_at,omitempty"`
	Redemptions    int              `json:"redemptions"`
	MaxRedemptions int              `json:"max_redemptions"`
	// SessionAlive reports whether the guest's tmux session is currently live.
	SessionAlive bool `json:"session_alive"`
	// Observers is how many read-only (operator watch) tmux clients are
	// attached right now.
	Observers int `json:"observers"`
	// Observable is false when this host has no multiplexer at all, so a
	// redeemed session (past or future) can never be watched or killed via
	// tmux — a host-wide capability, not a per-grant one.
	Observable bool `json:"observable"`
}

// newShareSessionView builds a view of g, querying tmux for its live
// attachment state.
func newShareSessionView(g jit.Grant, observable bool) shareSessionView {
	mux := shareGuestMuxName(g.ID)
	observers, alive := shareObserverCount(mux)
	return shareSessionView{
		GrantID:        g.ID,
		Resource:       g.Resource,
		Actor:          g.Actor,
		Recipient:      g.Recipient,
		Capabilities:   g.Capabilities,
		CreatedAt:      g.CreatedAt,
		ExpiresAt:      g.ExpiresAt,
		Redemptions:    g.Redemptions,
		MaxRedemptions: g.MaxRedemptions,
		SessionAlive:   alive,
		Observers:      observers,
		Observable:     observable,
	}
}

// shareEligibleGrant reports whether g is the kind of grant that has, or
// could have, an observable guest session: an approved grant offering a
// browser-terminal shell. Pending/denied/revoked grants, or ones that only
// ever offer a certificate, never redeem into a guest session.
func shareEligibleGrant(g jit.Grant) bool {
	return g.Status == jit.StatusApproved && jitOffersWeb(g)
}

// shareHostObservable reports whether this host can ever make a guest's
// access-request session observable: handleJITRedeemTerminal only runs the
// guest's shell inside tmux when shareMuxAvailable() is true at redeem time —
// with no tmux, the guest gets an ordinary, unobserved shell instead. This is
// a HOST capability, not a per-grant one, so it is checked fresh (a single
// LookPath, via shareMuxAvailable) rather than stored anywhere.
func shareHostObservable() bool {
	return shareMuxAvailable()
}

// shareObserverCount counts the READ-ONLY tmux clients attached to mux — an
// operator watching this guest session live (see handleShareWatch). The
// guest's own client always attaches read-write, so #{client_readonly}
// reliably discriminates an observer from the guest. mux is re-validated
// here regardless of whether the caller already did, so the "validated
// before argv" / "#nosec G204" invariant holds independent of the call site.
// Every tmux call goes through tmuxRunGuest, the package's BOUNDED (2s)
// runner, since this is reachable from a plain polling GET — a wedged tmux
// server must not be able to hang the request. A tmux error (no server,
// session gone) is reported as session_alive:false, observers:0, never a
// 500.
func shareObserverCount(mux string) (observers int, alive bool) {
	if !validHoneyMuxSessionName(mux) {
		return 0, false
	}
	out, err := tmuxRunGuest("list-clients", "-t", mux, "-F", "#{client_readonly}")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == "1" {
			observers++
		}
	}
	return observers, true
}

// handleListShareSessions lists every non-terminal grant that has, or could
// have, a guest access-request session (shareEligibleGrant): an approved
// grant offering a browser-terminal shell. Approved-but-expired grants are
// deliberately kept in this list rather than filtered further — the entire
// point of this panel is to surface a guest who is STILL attached after the
// grant's window or single-use redemption is spent, so an operator can see
// and kill it. session_alive/observers (queried live from tmux) convey the
// actual truth regardless of the grant's own window.
// @Summary List access-request guest sessions
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
		if shareEligibleGrant(g) {
			shares = append(shares, g)
		}
	}
	page, perPage := paginateParams(r)
	paged, total := paginateSlice(shares, page, perPage)

	observable := shareHostObservable()
	views := make([]shareSessionView, 0, len(paged))
	for _, g := range paged {
		views = append(views, newShareSessionView(g, observable))
	}
	writeJSON(w, map[string]any{"sessions": views, "total": total, "page": page, "per_page": perPage})
}

// shareKillResponse is the POST .../kill response.
type shareKillResponse struct {
	GrantID string `json:"grant_id"`
	// SessionKilled reports whether the guest's tmux session was actually
	// alive (and just terminated) by this call.
	SessionKilled bool `json:"session_killed"`
}

// shareKillGuestSession terminates the guest's own tmux session (never an
// operator's, never anything else) and reports whether it was actually alive
// (tmux's own `kill-session` fails when the target doesn't exist, so a single
// bounded call — via tmuxRunGuest, same invariant as shareObserverCount — both
// performs the kill and answers "was it alive"). kill-session naturally drops
// every attached client, guest and observers alike — there is no separate
// "detach observers" step to get wrong.
func shareKillGuestSession(mux string) (wasAlive bool) {
	if !validHoneyMuxSessionName(mux) {
		return false
	}
	_, err := tmuxRunGuest("kill-session", "-t", mux)
	return err == nil
}

// handleKillShareSession inverts the old "watch the operator" model: it
// revokes the grant (so the code can never be redeemed again) AND terminates
// the GUEST's own tmux session — here, `tmux kill-session -t
// honey_share_<grant>` is exactly right, since that session belongs to the
// guest, not an operator. Any observer currently watching is disconnected as
// a side effect of the session going away. Idempotent: killing an
// already-revoked or already-dead share is a 200 with session_killed:false,
// not an error, so a retried or double-clicked kill never surprises the
// operator.
// @Summary Kill an access-request guest session (revoke + terminate)
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
	if !jitOffersWeb(g) {
		httpError(w, fmt.Errorf("grant %q does not offer a guest session", id), http.StatusBadRequest)
		return
	}

	// Revoke so the code can never be redeemed again. An already-terminal
	// grant (already revoked/denied/expired) is exactly the idempotent retry
	// case, not a failure — proceed to the (best-effort) kill below either
	// way.
	if _, err := s.opts.Jit.Revoke(id, actor); err != nil {
		switch {
		case errors.Is(err, jit.ErrGrantNotActive):
			// already terminal — idempotent, fall through to kill.
		case errors.Is(err, jit.ErrGrantNotFound):
			httpError(w, err, http.StatusNotFound)
			return
		default:
			httpError(w, err, http.StatusInternalServerError)
			return
		}
	}

	mux := shareGuestMuxName(g.ID)
	killed := shareKillGuestSession(mux)

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "share_session_killed",
		Target:     mux,
		Decision:   "deny",
		ApprovalID: id,
	})

	writeJSON(w, shareKillResponse{GrantID: id, SessionKilled: killed})
}
