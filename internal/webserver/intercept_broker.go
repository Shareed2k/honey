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
	// StopByToken tears down the session identified by id, authenticating the
	// caller with the per-session agent token (the capability the CLI already
	// holds from the authorize response) instead of an actor identity. This
	// keeps graceful teardown working for cluster-scoped identity policies and
	// for sessions that outlive the id_token that originally authorized them.
	StopByToken(ctx context.Context, id, token, reason string) error
}

// handleInterceptConfig reports whether interception is available from the web
// UI and the operator-configured default modes. "enabled" tracks the browser
// terminal (the /ws/intercept session factory), which is what the Intercept
// button drives — not the CLI broker; the two enable independently now that the
// web terminal resolves its cluster from the pod record. Non-secret; mirrors
// handleOIDCConfig.
func (s *Server) handleInterceptConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"enabled":      s.opts.InterceptSessionFactory != nil,
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

// handleInterceptStop tears down the session named by the id URL param.
//
// Preferred path: the request carries the per-session agent token (the
// capability the CLI already holds from the authorize response). The token
// alone authenticates the stop — StopByToken hashes it and constant-time
// compares against the stored hash — so no id_token verification or identity
// resolution happens on this path. This is what lets a session be torn down
// gracefully even under a cluster-scoped identity policy or after the id_token
// that originally authorized it has expired.
//
// Fallback path (documented, kept for compatibility): when token is absent,
// the request is authenticated the old way — verify the id_token, resolve the
// actor via the identity policy, and stop only if that actor owns the
// session.
//
// Either path maps a broker error (unknown session, invalid token, or
// not-owned) to a generic 404 so as not to reveal which case applied. Never
// logs id_token or token material.
func (s *Server) handleInterceptStop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Token   string `json:"token"`
		IDToken string `json:"id_token"`
		Nonce   string `json:"nonce"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}

	if body.Token != "" {
		if err := s.interceptBroker.StopByToken(r.Context(), id, body.Token, "completed"); err != nil {
			http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
