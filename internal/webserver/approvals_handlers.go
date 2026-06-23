package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

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
