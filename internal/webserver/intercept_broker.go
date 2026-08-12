package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/shareed2k/honey/internal/intercept"
)

// interceptBroker is the server-side interception lifecycle the brokered
// endpoints drive. *intercept.Broker satisfies it; tests inject a stub.
type interceptBroker interface {
	Authorize(ctx context.Context, req intercept.AuthorizeRequest) (*intercept.BrokeredSession, error)
	Stop(ctx context.Context, id, actor, reason string) error
}

// handleInterceptConfig reports whether brokered interception is enabled and
// the operator-configured default modes. Non-secret; mirrors handleOIDCConfig.
func (s *Server) handleInterceptConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"enabled":      s.interceptBroker != nil,
		"default_mode": s.opts.InterceptDefaultMode,
	})
}

// handleInterceptAuthorize verifies an id_token, maps it to an actor via the
// identity policy, and asks the broker to gate (with full claims), deploy the
// agent, and return the session handle. Fail-closed: 401 on verify failure,
// 403 on a denied identity or a broker authorize failure (gate denial or
// deploy error alike — both generic to the client). Never logs id_token,
// token, or claim material.
func (s *Server) handleInterceptAuthorize(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDToken    string   `json:"id_token"`
		Nonce      string   `json:"nonce"`
		Cluster    string   `json:"cluster"`
		Namespace  string   `json:"namespace"`
		Pod        string   `json:"pod"`
		Container  string   `json:"container"`
		Mode       []string `json:"mode"`
		UDP        bool     `json:"udp"`
		Target     string   `json:"target"`
		AgentImage string   `json:"agent_image"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}

	claims, err := s.oidcVerifier.Verify(r.Context(), body.IDToken, body.Nonce)
	if err != nil {
		http.Error(w, `{"error":"invalid id_token"}`, http.StatusUnauthorized)
		return
	}

	identity, err := s.resolveIdentity(r.Context(), "intercept", body.Cluster, claims)
	if err != nil {
		s.auditLogin(r.Context(), "intercept_authorize", body.Cluster, claims.Email, "deny")
		http.Error(w, `{"error":"access forbidden by identity policy"}`, http.StatusForbidden)
		return
	}

	modes, perr := intercept.ParseModes(body.Mode)
	if perr != nil {
		httpError(w, fmt.Errorf("invalid mode: %w", perr), http.StatusBadRequest)
		return
	}

	sess, err := s.interceptBroker.Authorize(r.Context(), intercept.AuthorizeRequest{
		Actor:      identity.User,
		Subject:    claims.Subject,
		Email:      claims.Email,
		Groups:     claims.Groups,
		Claims:     claims.Raw,
		Cluster:    body.Cluster,
		Namespace:  body.Namespace,
		Pod:        body.Pod,
		Container:  body.Container,
		Modes:      modes,
		UDP:        body.UDP,
		Target:     body.Target,
		AgentImage: body.AgentImage,
	})
	if err != nil {
		// Gate denial vs deploy failure: both generic to the client.
		http.Error(w, `{"error":"intercept not authorized"}`, http.StatusForbidden)
		return
	}

	writeJSON(w, map[string]any{
		"session_id":   sess.ID,
		"token":        sess.Token,
		"control_port": sess.ControlPort,
		"egress_port":  sess.EgressPort,
		"expires_at":   sess.ExpiresAt,
	})
}

// handleInterceptStop verifies the id_token, resolves the actor, and asks the
// broker to stop the session — only if the actor owns it. An unknown or
// not-owned session yields 404. Never logs id_token or token material.
func (s *Server) handleInterceptStop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		IDToken string `json:"id_token"`
		Nonce   string `json:"nonce"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}

	claims, err := s.oidcVerifier.Verify(r.Context(), body.IDToken, body.Nonce)
	if err != nil {
		http.Error(w, `{"error":"invalid id_token"}`, http.StatusUnauthorized)
		return
	}

	actor := claims.Email
	if resolved, ierr := s.resolveIdentity(r.Context(), "intercept", "", claims); ierr == nil {
		actor = resolved.User
	}

	if err := s.interceptBroker.Stop(r.Context(), id, actor, "completed"); err != nil {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
