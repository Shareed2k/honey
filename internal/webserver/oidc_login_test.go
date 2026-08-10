package webserver

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/oidc"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/sshca"
)

// stubVerifier is a test idTokenVerifier returning canned claims / error so the
// login handlers can be exercised without a live identity provider.
type stubVerifier struct {
	claims oidc.Claims
	err    error
}

func (v stubVerifier) Verify(_ context.Context, _, _ string) (oidc.Claims, error) {
	return v.claims, v.err
}

// hasAuditEvent reports whether sink captured an event matching the fields
// (captureSink is defined in audit_test.go and reused here).
func hasAuditEvent(sink *captureSink, action, actor, decision string) bool {
	for _, e := range sink.all() {
		if e.Action == action && e.Actor == actor && e.Decision == decision {
			return true
		}
	}
	return false
}

// newCSRPEM returns a fresh ECDSA P-256 certificate-request in PEM form.
func newCSRPEM(t *testing.T, cn string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.CreateCertificateRequest(rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

// newSSHPubKeyLine returns a freshly generated ed25519 SSH public key line.
func newSSHPubKeyLine(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshPub, err := ssh.NewPublicKey(pub)
	require.NoError(t, err)
	return string(ssh.MarshalAuthorizedKey(sshPub))
}

// newOIDCTestServer builds a Server wired with real device/ssh CAs, the given
// identity enforcer, a stub verifier, and a capturing audit sink.
func newOIDCTestServer(t *testing.T, enf *policy.Enforcer, v idTokenVerifier, sink audit.Sink) *Server {
	t.Helper()
	deviceCA, err := LoadOrCreateDeviceCA(t.TempDir())
	require.NoError(t, err)
	sshCA, err := sshca.LoadOrCreateCA(t.TempDir())
	require.NoError(t, err)
	return &Server{
		opts:          Options{Enforcer: enf, AuditSink: sink},
		deviceCA:      deviceCA,
		sshCA:         sshCA,
		oidcVerifier:  v,
		deviceCertTTL: time.Hour,
		stateDir:      t.TempDir(),
	}
}

const kubeIdentityPolicy = `package honey
import rego.v1
identity := {"user": "alice@corp", "groups": ["developers"]} if {
	input.action == "identity"
	input.target == "kube"
	"eng" in input.groups
}
default allow := false
allow if { input.action == "identity"; identity }`

const sshIdentityPolicy = `package honey
import rego.v1
identity := {"user": "alice@corp", "principals": ["ubuntu", "alice@corp"]} if {
	input.action == "identity"
	input.target == "ssh"
	"eng" in input.groups
}
default allow := false
allow if { input.action == "identity"; identity }`

func TestOIDCKubeLogin_Happy(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", kubeIdentityPolicy)
	require.NoError(t, err)

	sink := &captureSink{}
	s := newOIDCTestServer(t, enf,
		stubVerifier{claims: oidc.Claims{Subject: "u-1", Email: "alice@corp", Groups: []string{"eng"}}}, sink)

	body, _ := json.Marshal(map[string]any{
		"id_token": "tok", "nonce": "N", "csr": newCSRPEM(t, "client"), "cluster": "prod",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kube/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleKubeLogin(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		CN      string   `json:"cn"`
		Groups  []string `json:"groups"`
		Cert    string   `json:"cert"`
		ProxyCA string   `json:"proxy_ca"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "alice@corp", out.CN)
	require.Equal(t, []string{"developers"}, out.Groups)

	block, _ := pem.Decode([]byte(out.Cert))
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	require.Equal(t, "alice@corp", cert.Subject.CommonName)
	require.ElementsMatch(t, []string{"developers"}, cert.Subject.Organization)

	require.True(t, hasAuditEvent(sink, "kube_login", "alice@corp", "allow"), "expected kube_login allow audit event")
}

func TestOIDCKubeLogin_Deny(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", kubeIdentityPolicy)
	require.NoError(t, err)

	sink := &captureSink{}
	// groups that map to no identity → policy denies.
	s := newOIDCTestServer(t, enf,
		stubVerifier{claims: oidc.Claims{Subject: "u-2", Email: "bob@corp", Groups: []string{"other"}}}, sink)

	body, _ := json.Marshal(map[string]any{
		"id_token": "tok", "nonce": "N", "csr": newCSRPEM(t, "client"), "cluster": "prod",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kube/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleKubeLogin(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.NotContains(t, rec.Body.String(), "CERTIFICATE")
	require.True(t, hasAuditEvent(sink, "kube_login", "bob@corp", "deny"), "expected kube_login deny audit event")
}

func TestOIDCKubeLogin_VerifyFailure(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", kubeIdentityPolicy)
	require.NoError(t, err)

	s := newOIDCTestServer(t, enf, stubVerifier{err: fmt.Errorf("bad signature")}, &captureSink{})

	body, _ := json.Marshal(map[string]any{
		"id_token": "tok", "nonce": "N", "csr": newCSRPEM(t, "client"), "cluster": "prod",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/kube/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleKubeLogin(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.NotContains(t, rec.Body.String(), "CERTIFICATE")
}

func TestOIDCSSHLogin_Happy(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", sshIdentityPolicy)
	require.NoError(t, err)

	sink := &captureSink{}
	s := newOIDCTestServer(t, enf,
		stubVerifier{claims: oidc.Claims{Subject: "u-1", Email: "alice@corp", Groups: []string{"eng"}}}, sink)

	body, _ := json.Marshal(map[string]any{
		"id_token": "tok", "nonce": "N", "public_key": newSSHPubKeyLine(t),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ssh/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSSHLogin(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var out struct {
		CN         string   `json:"cn"`
		Principals []string `json:"principals"`
		Cert       string   `json:"cert"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "alice@corp", out.CN)
	require.Equal(t, []string{"ubuntu", "alice@corp"}, out.Principals)

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(out.Cert))
	require.NoError(t, err)
	cert, ok := pub.(*ssh.Certificate)
	require.True(t, ok, "returned cert must parse as an *ssh.Certificate")
	require.Equal(t, []string{"ubuntu", "alice@corp"}, cert.ValidPrincipals)
	require.Equal(t, "alice@corp", cert.KeyId)

	require.True(t, hasAuditEvent(sink, "ssh_login", "alice@corp", "allow"), "expected ssh_login allow audit event")
}

func TestOIDCSSHLogin_EmptyPrincipalsDenied(t *testing.T) {
	// An identity that resolves a user but no principals is fail-closed (403):
	// the ssh CA requires at least one principal.
	enf, err := policy.NewFromSource(t.Context(), "id.rego", `package honey
import rego.v1
identity := {"user": "alice@corp"} if {
	input.action == "identity"
	input.target == "ssh"
	"eng" in input.groups
}
default allow := false
allow if { input.action == "identity"; identity }`)
	require.NoError(t, err)

	sink := &captureSink{}
	s := newOIDCTestServer(t, enf,
		stubVerifier{claims: oidc.Claims{Email: "alice@corp", Groups: []string{"eng"}}}, sink)

	body, _ := json.Marshal(map[string]any{
		"id_token": "tok", "nonce": "N", "public_key": newSSHPubKeyLine(t),
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ssh/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSSHLogin(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestOIDCConfig_ReportsPublicValues(t *testing.T) {
	s := &Server{opts: Options{OIDCPublic: &OIDCPublicConfig{
		Issuer:   "https://issuer.example",
		ClientID: "honey-kube",
		Scopes:   []string{"groups"},
	}}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/kube/oidc-config", nil)
	rec := httptest.NewRecorder()
	s.handleOIDCConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Issuer   string   `json:"issuer"`
		ClientID string   `json:"client_id"`
		Scopes   []string `json:"scopes"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Equal(t, "https://issuer.example", out.Issuer)
	require.Equal(t, "honey-kube", out.ClientID)
	require.Equal(t, []string{"groups"}, out.Scopes)
}

func TestOIDCLogin_DisabledReturns404(t *testing.T) {
	// No OIDCVerifier ⇒ the three SSO routes are never registered. DisableAuth
	// lets the request reach routing so an unmatched path returns 404 (not 401).
	s, err := NewServer(Options{DisableAuth: true, SearchRegistry: searchrun.NewRegistry(nil)})
	require.NoError(t, err)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/kube/oidc-config"},
		{http.MethodPost, "/api/v1/kube/login"},
		{http.MethodPost, "/api/v1/ssh/login"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code, "%s %s", tc.method, tc.path)
	}
}
