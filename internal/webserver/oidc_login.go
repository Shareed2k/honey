package webserver

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/k8sproxy"
	"github.com/shareed2k/honey/internal/oidc"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/sshca"
)

// OIDCPublicConfig carries the non-secret OIDC values the kube/ssh login command
// needs to start a browser sign-in. Populated from the oidc config block; nil ⇒
// the oidc-config endpoint reports empty values.
type OIDCPublicConfig struct {
	// Issuer is the OIDC issuer/discovery URL the client authenticates against.
	Issuer string
	// ClientID is the public client identifier (also the expected token audience).
	ClientID string
	// Scopes are additional scopes to request at login (openid is always implied).
	Scopes []string
}

// idTokenVerifier verifies an OIDC id_token and returns its claims. *oidc.Verifier
// satisfies it; tests inject a stub so the login handlers need no live provider.
type idTokenVerifier interface {
	Verify(ctx context.Context, rawIDToken, nonce string) (oidc.Claims, error)
}

// errIdentityForbidden is the fail-closed result of resolveIdentity when the
// identity policy resolves no identity for the subject. It carries no claim
// detail so it is safe to surface to the client.
var errIdentityForbidden = errors.New("access forbidden by identity policy")

// handleOIDCConfig returns the non-secret OIDC values the login command needs to
// start a browser sign-in. Registered only when SSO login is enabled.
func (s *Server) handleOIDCConfig(w http.ResponseWriter, _ *http.Request) {
	var issuer, clientID string
	var scopes []string
	if p := s.opts.OIDCPublic; p != nil {
		issuer = p.Issuer
		clientID = p.ClientID
		scopes = p.Scopes
	}
	writeJSON(w, map[string]any{
		"issuer":    issuer,
		"client_id": clientID,
		"scopes":    scopes,
	})
}

// resolveIdentity maps verified id_token claims to a gateway identity via the OPA
// identity policy. It is fail-closed: a nil Enforcer, an evaluation error, a
// denied decision, or a nil identity all yield errIdentityForbidden. target is
// "kube" or "ssh"; cluster is the requested cluster ("" for ssh).
func (s *Server) resolveIdentity(ctx context.Context, target, cluster string, claims oidc.Claims) (policy.IdentityResult, error) {
	if s.opts.Enforcer == nil {
		return policy.IdentityResult{}, errIdentityForbidden
	}
	d, err := s.opts.Enforcer.Evaluate(ctx, map[string]any{
		"action":  "identity",
		"target":  target,
		"cluster": cluster,
		"subject": claims.Subject,
		"email":   claims.Email,
		"groups":  claims.Groups,
		"claims":  claims.Raw,
	})
	if err != nil {
		return policy.IdentityResult{}, errIdentityForbidden
	}
	if !d.Allow || d.Identity == nil {
		return policy.IdentityResult{}, errIdentityForbidden
	}
	return *d.Identity, nil
}

// handleKubeLogin verifies an id_token, maps its verified claims to a Kubernetes
// identity via the OPA identity policy, and signs the submitted CSR into a
// short-lived mTLS client certificate (CN=user, O=groups) the access proxy
// consumes. It is authenticated by the id_token itself, so it mounts outside the
// session-auth group. Fail-closed: 401 on verification failure, 403 on a denied
// identity. Never logs id_token, certificate, or CSR material.
func (s *Server) handleKubeLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDToken string `json:"id_token"`
		Nonce   string `json:"nonce"`
		CSR     string `json:"csr"`
		Cluster string `json:"cluster"`
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

	id, err := s.resolveIdentity(r.Context(), "kube", body.Cluster, claims)
	if err != nil {
		s.auditLogin(r.Context(), "kube_login", body.Cluster, claims.Email, "deny")
		http.Error(w, `{"error":"access forbidden by identity policy"}`, http.StatusForbidden)
		return
	}

	block, _ := pem.Decode([]byte(body.CSR))
	if block == nil {
		httpError(w, fmt.Errorf("invalid CSR PEM"), http.StatusBadRequest)
		return
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		httpError(w, fmt.Errorf("parse CSR: %w", err), http.StatusBadRequest)
		return
	}

	certPEM, err := s.deviceCA.Sign(csr, id.User, id.Groups, s.deviceCertTTL)
	if err != nil {
		httpError(w, fmt.Errorf("sign client cert: %w", err), http.StatusBadRequest)
		return
	}

	s.auditLogin(r.Context(), "kube_login", body.Cluster, id.User, "allow")

	resp := map[string]any{
		"cn":     id.User,
		"groups": id.Groups,
		"cert":   string(certPEM),
	}
	if proxyCA := s.proxyServingCAPEM(); proxyCA != "" {
		resp["proxy_ca"] = proxyCA
	}
	writeJSON(w, resp)
}

// handleSSHLogin verifies an id_token, maps its verified claims to SSH principals
// via the OPA identity policy, and signs the submitted SSH public key into a
// short-lived user certificate the ssh gateway consumes. Authenticated by the
// id_token itself, so it mounts outside the session-auth group. Fail-closed: 401
// on verification failure, 403 on a denied identity (including one that resolves
// no principals — the ssh CA requires at least one). Never logs id_token,
// certificate, or public-key material.
func (s *Server) handleSSHLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDToken   string `json:"id_token"`
		Nonce     string `json:"nonce"`
		PublicKey string `json:"public_key"`
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

	id, err := s.resolveIdentity(r.Context(), "ssh", "", claims)
	if err != nil || len(id.Principals) == 0 {
		s.auditLogin(r.Context(), "ssh_login", "", claims.Email, "deny")
		http.Error(w, `{"error":"access forbidden by identity policy"}`, http.StatusForbidden)
		return
	}

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey))
	if err != nil {
		httpError(w, fmt.Errorf("parse public key: %w", err), http.StatusBadRequest)
		return
	}

	cert, err := s.sshCA.Sign(sshca.SignRequest{
		PublicKey:  pub,
		KeyID:      id.User,
		Principals: id.Principals,
		TTL:        s.deviceCertTTL,
	})
	if err != nil {
		httpError(w, fmt.Errorf("sign certificate: %w", err), http.StatusBadRequest)
		return
	}

	s.auditLogin(r.Context(), "ssh_login", "", id.User, "allow")

	writeJSON(w, map[string]any{
		"cn":         id.User,
		"principals": id.Principals,
		"cert":       string(ssh.MarshalAuthorizedKey(cert)),
	})
}

// proxyServingCAPEM returns the k8s access proxy's serving-CA certificate in PEM
// form when it exists under the server's state dir, or "" when absent or
// unreadable. Best-effort: a missing file is not an error (the client falls back
// to its own trust configuration).
func (s *Server) proxyServingCAPEM() string {
	if s.stateDir == "" {
		return ""
	}
	pemBytes, err := safepath.ReadFile(k8sproxy.ServingCAPath(s.stateDir))
	if err != nil {
		return ""
	}
	return string(pemBytes)
}

// auditLogin records one SSO login decision. It logs only the action, target
// cluster, resolved actor, and allow/deny — never id_token, certificate, CSR, or
// public-key material.
func (s *Server) auditLogin(ctx context.Context, action, cluster, actor, decision string) {
	if s.opts.AuditSink == nil {
		return
	}
	_ = s.opts.AuditSink.Log(ctx, audit.Event{
		Source:   "web",
		Actor:    actor,
		Action:   action,
		Target:   cluster,
		Decision: decision,
	})
}
