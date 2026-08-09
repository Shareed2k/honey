package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/hosts"
)

// gateInteractiveSession asks OPA whether the request's actor may open an
// interactive shell on the target (action "interactive_session"). A nil
// enforcer always allows.
func (s *Server) gateInteractiveSession(r *http.Request, rec hosts.Record) error {
	return s.evalInteractiveSession(r.Context(), userFromRequest(r, s.opts.TrustedProxyNets, s.opts.JWTPubKey), rec)
}

// evalInteractiveSession asks OPA whether actor may open an interactive shell
// on rec (action "interactive_session"). A nil enforcer always allows. Unlike
// gateInteractiveSession it takes the actor explicitly, so callers with no
// request session (e.g. a share-link recipient) can gate with a derived
// identity.
func (s *Server) evalInteractiveSession(ctx context.Context, actor string, rec hosts.Record) error {
	if s.opts.Enforcer == nil {
		return nil
	}
	d, err := s.opts.Enforcer.Evaluate(ctx, map[string]any{
		"action": "interactive_session",
		"actor":  actor,
		"target": map[string]any{
			"name":     rec.Name,
			"provider": rec.Provider,
			"env":      rec.Meta["env"],
			"groups":   rec.Groups,
		},
	})
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if !d.Allow {
		return fmt.Errorf("%s", reasonOrForbidden(d.DenyReason))
	}
	return nil
}

func reasonOrForbidden(s string) string {
	if s == "" {
		return "forbidden by policy"
	}
	return s
}

// handleListApprovals returns the current pending/decided approval records.
func (s *Server) handleListApprovals(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Approvals == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"approvals": []any{}})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"approvals": s.opts.Approvals.List()})
}

// approvalDecisionRequest is the POST body for deciding an approval.
type approvalDecisionRequest struct {
	Decision string `json:"decision"` // "approve" | "deny"
}

// handleDecideApproval approves or denies a pending run. The approver is the
// request actor; an OPA "recipe_approve" policy (when configured) may restrict
// who can approve (e.g. approver must differ from the requester).
func (s *Server) handleDecideApproval(w http.ResponseWriter, r *http.Request) {
	if s.opts.Approvals == nil {
		httpError(w, fmt.Errorf("approvals not enabled"), http.StatusNotFound)
		return
	}
	id := chi.URLParam(r, "id")
	rec, ok := s.opts.Approvals.Get(id)
	if !ok {
		httpError(w, fmt.Errorf("approval %q not found", id), http.StatusNotFound)
		return
	}

	var body approvalDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	approve := body.Decision == "approve"

	approver := actorFromCtx(r.Context())
	if approve && !s.approverAllowed(r, approver, rec.Actor, rec.Recipe) {
		http.Error(w, `{"error":"approver not permitted"}`, http.StatusForbidden)
		return
	}

	decided, err := s.opts.Approvals.Decide(id, approver, approve)
	if err != nil {
		httpError(w, err, http.StatusConflict)
		return
	}

	decision := "deny"
	if approve {
		decision = "allow"
	}
	_ = s.opts.AuditSink.Log(r.Context(), audit.Event{
		Source:     "web",
		Actor:      approver,
		Action:     "approval_decided",
		Target:     decided.Recipe,
		Decision:   decision,
		ApprovalID: id,
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(decided)
}

// approverAllowed consults the OPA "recipe_approve" policy. With no enforcer the
// default is permissive (any authenticated actor may approve).
func (s *Server) approverAllowed(r *http.Request, approver, requester, recipe string) bool {
	if s.opts.Enforcer == nil {
		return true
	}
	d, err := s.opts.Enforcer.Evaluate(r.Context(), map[string]any{
		"action":    "recipe_approve",
		"actor":     approver,
		"approver":  approver,
		"requester": requester,
		"recipe":    recipe,
	})
	if err != nil {
		return false
	}
	return d.Allow
}
