package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

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
)

// jitResourceRequest is the wire shape of a grant's target resource.
type jitResourceRequest struct {
	Name      string            `json:"name"`
	Provider  string            `json:"provider,omitempty"`
	PrimaryIP string            `json:"primary_ip,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// jitCreateGrantRequest is the POST /jit/grants body.
type jitCreateGrantRequest struct {
	Resource        jitResourceRequest `json:"resource"`
	Capabilities    []jit.Capability   `json:"capabilities"`
	Delivery        jit.Delivery       `json:"delivery"`
	Duration        string             `json:"duration"`
	Reason          string             `json:"reason,omitempty"`
	RequireApproval bool               `json:"require_approval,omitempty"`
	MaxRedemptions  int                `json:"max_redemptions,omitempty"`
	Recipient       string             `json:"recipient,omitempty"`
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

	duration := jitDefaultDuration
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
	if duration > jitMaxDuration {
		duration = jitMaxDuration
	}

	actor := actorFromCtx(r.Context())
	resource := jit.ResourceRef{
		Name:      body.Resource.Name,
		Provider:  body.Resource.Provider,
		PrimaryIP: body.Resource.PrimaryIP,
		Meta:      body.Resource.Meta,
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
		"id":               stored.ID,
		"code":             code,
		"link_path":        "/access/" + code,
		"status":           string(stored.Status),
		"require_approval": stored.RequireApproval,
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
	d, err := s.opts.Enforcer.Evaluate(r.Context(), map[string]any{
		"action": "jit_grant",
		"actor":  actor,
		"target": map[string]any{
			"name":     resource.Name,
			"provider": resource.Provider,
			"env":      resource.Meta["env"],
			"groups":   groups,
		},
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

// handleListJITGrants returns every stored grant as a redacted jitGrantView —
// never the code hash, never the plaintext code.
func (s *Server) handleListJITGrants(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Jit == nil {
		httpError(w, fmt.Errorf("jit not enabled"), http.StatusServiceUnavailable)
		return
	}
	grants := s.opts.Jit.List()
	views := make([]jitGrantView, 0, len(grants))
	for _, g := range grants {
		views = append(views, newJitGrantView(g))
	}
	writeJSON(w, map[string]any{"grants": views})
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
