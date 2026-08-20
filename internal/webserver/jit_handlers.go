package webserver

import (
	"context"
	"encoding/json"
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
	"github.com/shareed2k/honey/internal/recipenotify"
)

const (
	// jitDefaultDuration is the access window used when a create request omits
	// a duration.
	jitDefaultDuration = time.Hour
	// jitMaxDuration caps how long a JIT grant's access window may last,
	// regardless of what the caller requests. Requests above the cap are
	// clamped down, not rejected.
	jitMaxDuration = 24 * time.Hour
	// jitNotifyTimeout bounds the best-effort notify send on grant creation so
	// a slow/unreachable notify backend cannot stall the request.
	jitNotifyTimeout = 5 * time.Second
	// jitDefaultPerPage and jitMaxPerPage bound ?per_page= on the paginated
	// list endpoints (/jit/grants, /share/sessions): default when omitted,
	// hard cap regardless of what the caller requests.
	jitDefaultPerPage = 50
	jitMaxPerPage     = 200
)

// paginateParams parses the 1-based ?page= and ?per_page= query params
// shared by every paginated list endpoint, defaulting/clamping per_page to
// [1, jitMaxPerPage] and page to >= 1. Malformed or missing values fall back
// to the defaults rather than erroring — pagination is a display convenience,
// not a validated input a bad value should 400 over.
func paginateParams(r *http.Request) (page, perPage int) {
	page = 1
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("page"))); err == nil && v > 0 {
		page = v
	}
	perPage = jitDefaultPerPage
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("per_page"))); err == nil && v > 0 {
		perPage = v
	}
	if perPage > jitMaxPerPage {
		perPage = jitMaxPerPage
	}
	return page, perPage
}

// paginateSlice returns the page-th (1-based) perPage-sized window of items
// (already sorted by the caller) plus the total item count. An out-of-range
// page returns an empty, non-nil slice rather than erroring.
func paginateSlice[T any](items []T, page, perPage int) ([]T, int) {
	total := len(items)
	start := (page - 1) * perPage
	if start < 0 || start >= total {
		return []T{}, total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return items[start:end], total
}

// jitResourceRequest is the wire shape of a grant's target resource.
type jitResourceRequest struct {
	Name      string            `json:"name"`
	Provider  string            `json:"provider,omitempty"`
	PrimaryIP string            `json:"primary_ip,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// jitCreateGrantRequest is the POST /jit/grants body.
//
// Kind/MuxSession/Capability are the live-terminal share extension: when Kind
// is "live_terminal", Capability ("watch" or "collaborate") replaces
// Capabilities, Delivery is forced to web, and MuxSession is validated here
// (this package, unlike internal/jit, can import both mux-name validators)
// before it lands in the grant's ResourceRef.Meta. Every other field keeps its
// normal meaning and is unaffected by Kind.
type jitCreateGrantRequest struct {
	Kind            string             `json:"kind,omitempty"`
	MuxSession      string             `json:"mux_session,omitempty"`
	Capability      jit.Capability     `json:"capability,omitempty"`
	Resource        jitResourceRequest `json:"resource"`
	Capabilities    []jit.Capability   `json:"capabilities"`
	Delivery        jit.Delivery       `json:"delivery"`
	Duration        string             `json:"duration"`
	Reason          string             `json:"reason,omitempty"`
	RequireApproval bool               `json:"require_approval,omitempty"`
	MaxRedemptions  int                `json:"max_redemptions,omitempty"`
	Recipient       string             `json:"recipient,omitempty"`
}

// jitKindLiveTerminal marks a grant request that attaches the redeemer to an
// operator's EXISTING tmux-backed session instead of a brand-new shell.
const jitKindLiveTerminal = "live_terminal"

// applyLiveTerminalShare validates a live_terminal share request and rewrites
// resource + body in place for the rest of grant creation: the mux session
// name is validated HERE — internal/jit cannot import validHoneyMuxSessionName
// / validInterceptMuxName without an import cycle, so this is the one place
// that gates a session name before it is ever handed to a guest to attach to
// — then Capability replaces Capabilities and Delivery is forced to web (a
// live-terminal attach only exists over the browser terminal, never the SSH
// certificate path).
//
// The caller (handleCreateJITGrant) must invoke this whenever EITHER the
// top-level Kind or body.Resource.Meta["kind"] equals jitKindLiveTerminal
// (MED-2): Meta is copied into resource verbatim before this ever runs, so a
// request that set the kind only inside Meta used to skip this entire gate —
// an unvalidated mux_session reaching a guest's attach untouched.
//
// requester is the actor creating the grant (handleCreateJITGrant's
// actorFromCtx). For the honey-int-* (intercept resume) family it is
// cross-checked (MED-3) against the HONEY_INT_ACTOR interceptResumeSetMeta
// recorded for that session, refusing a live share whose actor doesn't
// match. Unlike round 1, an UNKNOWN owner now fails closed for this family
// (interceptSessionActorRetry bounds a short retry against
// interceptResumeSetMeta's own write race first): those names are derivable
// cross-tenant and the intercept list is visible to any authenticated user,
// so "we couldn't determine the owner" must mean "deny", not "allow". honey_*
// (plain SSH/docker/k8s web-terminal) sessions carry no such recorded owner
// at all — that gap is real, stays "unknown ⇒ allow", and is left as a
// follow-up, not invented here as a new store.
func applyLiveTerminalShare(resource *jit.ResourceRef, body *jitCreateGrantRequest, requester string) error {
	mux := strings.TrimSpace(body.MuxSession)
	if mux == "" {
		return errors.New("mux_session is required for a live_terminal share")
	}
	if !validHoneyMuxSessionName(mux) && !validInterceptMuxName(mux) {
		return fmt.Errorf("invalid mux_session %q", mux)
	}
	// MED-4: grant creation must not succeed for a tab with no live tmux
	// session (zellij preferred, no-mux fallback, pve-serial/truenas tabs never
	// have one) — otherwise the guest only discovers the broken link after
	// burning a redemption. A live-share grant is only ever redeemable on the
	// honey node that actually holds this tmux session — it does not follow
	// the session elsewhere.
	//
	// NEW-16 (round 3): checked BEFORE the ownership block below, not after —
	// round 2 had it last, so a dead honey-int-* session paid for the
	// ownership retry's full cost (6 execs, ~500ms) before returning "owner
	// could not be determined" instead of this friendlier, cheaper message.
	if !tmuxGuestSessionAlive(mux) {
		return errors.New("this terminal is not shareable — no live tmux session")
	}
	if validInterceptMuxName(mux) {
		owner := interceptSessionActorRetry(mux)
		if owner == "" {
			return fmt.Errorf("refusing to share session %q: owner could not be determined", mux)
		}
		if owner != requester {
			return fmt.Errorf("refusing to share session %q: not owned by %q", mux, requester)
		}
	}
	switch body.Capability {
	case jit.CapWatch, jit.CapCollab:
	default:
		return fmt.Errorf("capability must be %q or %q for a live_terminal share", jit.CapWatch, jit.CapCollab)
	}
	// NEW-3: tmux matches a `-t` target by PREFIX ("honey-int-abc" resolves to
	// a real "honey-int-abcdef"), so a request naming a unique prefix would
	// otherwise pass every check above and attach to the REAL session while
	// this stores (and later, policy/audit see) the ALIAS — an exact-match
	// policy rule is evadable and the audit trail would name a session that
	// does not exist. Resolve the actual tmux session name now and refuse
	// anything but an exact match; only the canonical name is ever stored.
	canonicalMux, err := tmuxCanonicalSessionName(mux)
	if err != nil {
		return fmt.Errorf("resolve mux_session: %w", err)
	}
	mux = canonicalMux

	meta := make(map[string]string, len(resource.Meta)+2)
	for k, v := range resource.Meta {
		meta[k] = v
	}
	meta["kind"] = jitKindLiveTerminal
	meta["mux_session"] = mux
	resource.Meta = meta

	body.Capabilities = []jit.Capability{body.Capability}
	body.Delivery = jit.DeliveryWeb
	return nil
}

// jitGrantView is the redacted, wire-safe view of a jit.Grant. It deliberately
// omits CodeHash and never carries the plaintext redeem code — that is
// returned only once, from handleCreateJITGrant's response.
type jitGrantView struct {
	ID              string           `json:"id"`
	Actor           string           `json:"actor"`
	Recipient       string           `json:"recipient,omitempty"`
	Resource        jit.ResourceRef  `json:"resource"`
	Capabilities    []jit.Capability `json:"capabilities"`
	Delivery        jit.Delivery     `json:"delivery"`
	Duration        time.Duration    `json:"duration"`
	Reason          string           `json:"reason,omitempty"`
	Status          jit.Status       `json:"status"`
	RequireApproval bool             `json:"require_approval"`
	Approver        string           `json:"approver,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	DecidedAt       time.Time        `json:"decided_at,omitempty"`
	StartsAt        time.Time        `json:"starts_at,omitempty"`
	ExpiresAt       time.Time        `json:"expires_at,omitempty"`
	MaxRedemptions  int              `json:"max_redemptions"`
	Redemptions     int              `json:"redemptions"`
}

// newJitGrantView copies the display fields of g, leaving CodeHash behind.
func newJitGrantView(g jit.Grant) jitGrantView {
	return jitGrantView{
		ID:              g.ID,
		Actor:           g.Actor,
		Recipient:       g.Recipient,
		Resource:        g.Resource,
		Capabilities:    g.Capabilities,
		Delivery:        g.Delivery,
		Duration:        g.Duration,
		Reason:          g.Reason,
		Status:          g.Status,
		RequireApproval: g.RequireApproval,
		Approver:        g.Approver,
		CreatedAt:       g.CreatedAt,
		DecidedAt:       g.DecidedAt,
		StartsAt:        g.StartsAt,
		ExpiresAt:       g.ExpiresAt,
		MaxRedemptions:  g.MaxRedemptions,
		Redemptions:     g.Redemptions,
	}
}

// jitGrantDecisionRequest is the POST /jit/grants/{id} body.
type jitGrantDecisionRequest struct {
	Decision string `json:"decision"` // "approve" | "deny" | "revoke"
}

// handleCreateJITGrant creates a new time-boxed access grant. Direct grants
// (require_approval=false) come back Approved with an open redemption window;
// grants requiring approval come back Pending, and a best-effort notification
// is sent (never containing the redeem code).
func (s *Server) handleCreateJITGrant(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}

	var body jitCreateGrantRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}

	duration := s.jitDefaultDuration
	if raw := strings.TrimSpace(body.Duration); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			httpError(w, fmt.Errorf("parse duration: %w", err), http.StatusBadRequest)
			return
		}
		if d <= 0 {
			httpError(w, fmt.Errorf("duration must be positive"), http.StatusBadRequest)
			return
		}
		duration = d
	}
	if duration > s.jitMaxDuration {
		duration = s.jitMaxDuration
	}

	actor := actorFromCtx(r.Context())
	resource := jit.ResourceRef{
		Name:      body.Resource.Name,
		Provider:  body.Resource.Provider,
		PrimaryIP: body.Resource.PrimaryIP,
		Meta:      body.Resource.Meta,
	}

	// MED-2: trigger on either the top-level Kind or a meta-only kind — see
	// applyLiveTerminalShare's doc comment for why both must be checked.
	if body.Kind == jitKindLiveTerminal || body.Resource.Meta["kind"] == jitKindLiveTerminal {
		if err := applyLiveTerminalShare(&resource, &body, actor); err != nil {
			httpError(w, err, http.StatusBadRequest)
			return
		}
	}

	if err := s.gateJITGrant(r, actor, resource, body.Capabilities, body.Delivery, body.RequireApproval); err != nil {
		httpError(w, err, http.StatusForbidden)
		return
	}

	stored, code, err := s.opts.Jit.Create(jit.Grant{
		Actor:           actor,
		Recipient:       body.Recipient,
		Resource:        resource,
		Capabilities:    body.Capabilities,
		Delivery:        body.Delivery,
		Duration:        duration,
		Reason:          body.Reason,
		RequireApproval: body.RequireApproval,
		MaxRedemptions:  body.MaxRedemptions,
	})
	if err != nil {
		if errors.Is(err, jit.ErrInvalidGrant) {
			httpError(w, err, http.StatusBadRequest)
			return
		}
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	if stored.Status == jit.StatusPending {
		notifyJITPending(stored)
	}

	decision := "allow"
	if stored.Status == jit.StatusPending {
		decision = "require_approval"
	}
	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "jit_created",
		Target:     resource.Name,
		Decision:   decision,
		ApprovalID: stored.ID,
	})

	resp := map[string]any{
		"id":   stored.ID,
		"code": code,
		// Query form so a cold link click resolves to the SPA's index.html
		// through the static file server without a server-side path fallback;
		// the web UI reads ?access=<code>.
		"link_path":        "/?access=" + code,
		"status":           string(stored.Status),
		"require_approval": stored.RequireApproval,
	}
	// "link" is the absolute, reachable form of link_path — computed
	// server-side because the browser's own origin (e.g. localhost:8765) is
	// useless to a remote recipient such as a phone scanning the QR code. On
	// failure (no usable listen host and no resolvable LAN IP) it is simply
	// omitted; the web UI then falls back to window.location.origin +
	// link_path, same as before this existed.
	switch base, err := shareBaseURL(s.opts.PublicURL, s.opts.ListenAddr, defaultLANResolver); {
	case err == nil:
		resp["link"] = base + "/?access=" + code
	case errors.Is(err, ErrListenerLoopbackOnly):
		// The common default (--listen localhost:8765): the listener answers on
		// loopback only, so no absolute link could reach another device.
		// Substituting a LAN IP here would hand out a URL nothing is listening
		// on, so instead tell the operator what to change — the UI shows this
		// next to the (browser-origin) link so a QR code that cannot work is
		// never presented as if it could.
		resp["link_warning"] = err.Error()
	default:
		zap.L().Debug("jit: could not compute absolute share link, client will fall back to browser origin", zap.Error(err))
	}
	if !stored.ExpiresAt.IsZero() {
		resp["expires_at"] = stored.ExpiresAt.Format(time.RFC3339)
	}
	writeJSON(w, resp)
}

// notifyJITPending best-effort notifies configured backends that a JIT grant
// awaits approval. It never includes the redeem code or any other secret, and
// a send failure never fails the request that triggered it.
func notifyJITPending(stored jit.Grant) {
	n, ok := recipenotify.BuildFromEnv()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), jitNotifyTimeout)
	defer cancel()
	body := fmt.Sprintf("%s requests %s on %s for %s; approve id %s",
		stored.Actor, joinCapabilities(stored.Capabilities), stored.Resource.Name, stored.Duration, stored.ID)
	_ = n.Send(ctx, "honey: JIT access request "+stored.ID, body)
}

// joinCapabilities renders capabilities as a short comma-separated list for
// human-readable notification text.
func joinCapabilities(caps []jit.Capability) string {
	parts := make([]string, len(caps))
	for i, c := range caps {
		parts[i] = string(c)
	}
	return strings.Join(parts, ",")
}

// gateJITGrant asks OPA whether actor may create this grant (action
// "jit_grant"). A nil enforcer always allows.
func (s *Server) gateJITGrant(r *http.Request, actor string, resource jit.ResourceRef, caps []jit.Capability, delivery jit.Delivery, requireApproval bool) error {
	if s.opts.Enforcer == nil {
		return nil
	}
	var groups any
	if g, ok := resource.Meta["groups"]; ok {
		groups = g
	}
	target := map[string]any{
		"name":     resource.Name,
		"provider": resource.Provider,
		"env":      resource.Meta["env"],
		"groups":   groups,
	}
	// MED-3: a live_terminal grant's mux_session — the exact (now-canonical,
	// see applyLiveTerminalShare's NEW-3 fix) session a redeemer will be
	// attached to — so policy can see (and gate on) which live session is
	// being shared, not just the target host. Omitted entirely when empty
	// (every non-live grant), so an existing "jit_grant" policy sees the same
	// input shape it always has, key absent rather than an empty string.
	if mux := resource.Meta["mux_session"]; mux != "" {
		target["mux_session"] = mux
	}
	d, err := s.opts.Enforcer.Evaluate(r.Context(), map[string]any{
		"action":           "jit_grant",
		"actor":            actor,
		"target":           target,
		"capabilities":     caps,
		"delivery":         delivery,
		"require_approval": requireApproval,
	})
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if !d.Allow {
		return fmt.Errorf("%s", reasonOrForbidden(d.DenyReason))
	}
	return nil
}

// handleListJITGrants returns a page of stored grants as redacted
// jitGrantViews (never the code hash, never the plaintext code), newest-first,
// paginated via ?page=/?per_page= (see paginateParams).
func (s *Server) handleListJITGrants(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	grants := s.opts.Jit.List() // already newest-first, stable
	page, perPage := paginateParams(r)
	paged, total := paginateSlice(grants, page, perPage)
	views := make([]jitGrantView, 0, len(paged))
	for _, g := range paged {
		views = append(views, newJitGrantView(g))
	}
	writeJSON(w, map[string]any{"grants": views, "total": total, "page": page, "per_page": perPage})
}

// handleDeleteJITGrant permanently deletes one TERMINAL grant (denied,
// revoked, or expired). An ACTIVE grant is refused with 409: the operator
// must revoke (or kill, for a live-terminal share) it first, so a delete can
// never silently drop a live share's audit trail before it actually ends.
func (s *Server) handleDeleteJITGrant(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	id := chi.URLParam(r, "id")
	actor := actorFromCtx(r.Context())

	if err := s.opts.Jit.Delete(id); err != nil {
		switch {
		case errors.Is(err, jit.ErrGrantNotFound):
			httpError(w, err, http.StatusNotFound)
		case errors.Is(err, jit.ErrGrantNotTerminal):
			httpError(w, err, http.StatusConflict)
		default:
			httpError(w, err, http.StatusInternalServerError)
		}
		return
	}

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      actor,
		Action:     "jit_grant_deleted",
		Decision:   "allow",
		ApprovalID: id,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleJITGrantsPurge deletes every currently TERMINAL grant and returns how
// many were removed — the bulk "delete all finished grants" action. Active
// grants are never touched.
func (s *Server) handleJITGrantsPurge(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	actor := actorFromCtx(r.Context())

	n, err := s.opts.Jit.Purge()
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:   "web",
		Actor:    actor,
		Action:   "jit_grants_purged",
		Decision: "allow",
		Extra:    map[string]string{"deleted": strconv.Itoa(n)},
	})
	writeJSON(w, map[string]any{"deleted": n})
}

// handleDecideJITGrant approves, denies, or revokes a grant. Approving is
// gated by an OPA "jit_approve" policy (when configured), mirroring
// approverAllowed for recipe approvals.
func (s *Server) handleDecideJITGrant(w http.ResponseWriter, r *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	id := chi.URLParam(r, "id")

	var body jitGrantDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}

	g, ok := s.opts.Jit.Get(id)
	if !ok {
		httpError(w, fmt.Errorf("grant %q not found", id), http.StatusNotFound)
		return
	}

	approver := actorFromCtx(r.Context())

	var (
		decided       jit.Grant
		err           error
		auditAction   string
		auditDecision string
	)
	switch body.Decision {
	case "approve":
		if !s.jitApproverAllowed(r, approver, g.Actor, g.Resource.Name) {
			http.Error(w, `{"error":"approver not permitted"}`, http.StatusForbidden)
			return
		}
		decided, err = s.opts.Jit.Decide(id, approver, true)
		auditAction, auditDecision = "jit_decided", "allow"
	case "deny":
		decided, err = s.opts.Jit.Decide(id, approver, false)
		auditAction, auditDecision = "jit_decided", "deny"
	case "revoke":
		decided, err = s.opts.Jit.Revoke(id, approver)
		auditAction, auditDecision = "jit_revoked", "deny"
	default:
		httpError(w, fmt.Errorf("unknown decision %q", body.Decision), http.StatusBadRequest)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, jit.ErrGrantNotFound):
			httpError(w, err, http.StatusNotFound)
		case errors.Is(err, jit.ErrGrantNotActive):
			httpError(w, err, http.StatusConflict)
		default:
			httpError(w, err, http.StatusInternalServerError)
		}
		return
	}

	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      approver,
		Action:     auditAction,
		Target:     decided.Resource.Name,
		Decision:   auditDecision,
		ApprovalID: id,
	})

	writeJSON(w, newJitGrantView(decided))
}

// jitApproverAllowed consults the OPA "jit_approve" policy. With no enforcer
// the default is permissive (any authenticated actor may approve).
func (s *Server) jitApproverAllowed(r *http.Request, approver, requester, target string) bool {
	if s.opts.Enforcer == nil {
		return true
	}
	d, err := s.opts.Enforcer.Evaluate(r.Context(), map[string]any{
		"action":    "jit_approve",
		"actor":     approver,
		"approver":  approver,
		"requester": requester,
		"target":    target,
	})
	if err != nil {
		return false
	}
	return d.Allow
}
