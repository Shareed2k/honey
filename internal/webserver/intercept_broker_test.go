package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/oidc"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/sshca"
)

// stubBroker is a test interceptBroker recording the last AuthorizeRequest it
// received and returning canned results/errors so the brokered endpoints can
// be exercised without a real intercept.Broker (no cluster, no exec).
type stubBroker struct {
	authErr        error
	stopErr        error
	stopByTokenErr error
	lastReq        intercept.AuthorizeRequest
	// lastStopActor records the actor argument the handler passed to Stop, so
	// tests can prove it is the resolved identity (not a client-supplied value
	// or an unconditional email fallback).
	lastStopActor string
	// lastStopByTokenID/lastStopByTokenToken record the arguments the handler
	// passed to StopByToken, so tests can prove the handler forwards the
	// client-supplied token verbatim rather than falling back to id_token.
	lastStopByTokenID    string
	lastStopByTokenToken string
}

func (s *stubBroker) Authorize(_ context.Context, req intercept.AuthorizeRequest) (*intercept.BrokeredSession, error) {
	s.lastReq = req
	if s.authErr != nil {
		return nil, s.authErr
	}
	return &intercept.BrokeredSession{ID: "sess-1", Token: "tok", ControlPort: 30000, EgressPort: 30001}, nil
}

func (s *stubBroker) Stop(_ context.Context, _, actor, _ string) error {
	s.lastStopActor = actor
	return s.stopErr
}

func (s *stubBroker) StopByToken(_ context.Context, id, token, _ string) error {
	s.lastStopByTokenID = id
	s.lastStopByTokenToken = token
	return s.stopByTokenErr
}

// newBrokerTestServer builds a Server with the given identity enforcer, a stub
// verifier, and a stub broker, mirroring newOIDCTestServer in oidc_login_test.go.
func newBrokerTestServer(t *testing.T, enf *policy.Enforcer, v idTokenVerifier, br interceptBroker, sink audit.Sink) *Server {
	t.Helper()
	deviceCA, err := LoadOrCreateDeviceCA(t.TempDir())
	require.NoError(t, err)
	sshCA, err := sshca.LoadOrCreateCA(t.TempDir())
	require.NoError(t, err)
	return &Server{
		opts:            Options{Enforcer: enf, AuditSink: sink},
		deviceCA:        deviceCA,
		sshCA:           sshCA,
		oidcVerifier:    v,
		interceptBroker: br,
		deviceCertTTL:   time.Hour,
		stateDir:        t.TempDir(),
	}
}

// withURLParam injects a chi route context carrying the "id" URL param (the
// only one any handler in this file reads), so a handler that reads it via
// chi.URLParam can be invoked directly (no router).
func withURLParam(r *http.Request, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// interceptIdentityPolicy mirrors kubeIdentityPolicy (oidc_login_test.go) but
// for target "intercept": "eng" group members resolve to alice@corp/developers.
const interceptIdentityPolicy = `package honey
import rego.v1
identity := {"user": "alice@corp", "groups": ["developers"]} if {
	input.action == "identity"
	input.target == "intercept"
	"eng" in input.groups
}
default allow := false
allow if { input.action == "identity"; identity }`

func TestInterceptAuthorize_BadToken401(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", interceptIdentityPolicy)
	require.NoError(t, err)

	s := newBrokerTestServer(t, enf, stubVerifier{err: errors.New("bad signature")}, &stubBroker{}, &captureSink{})

	body, _ := json.Marshal(map[string]any{"id_token": "x", "cluster": "prod", "namespace": "n", "pod": "p"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/authorize", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleInterceptAuthorize(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestInterceptAuthorize_Allow200_PassesClaims(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", interceptIdentityPolicy)
	require.NoError(t, err)

	br := &stubBroker{}
	claims := oidc.Claims{Subject: "alice", Email: "alice@corp", Groups: []string{"eng"}, Raw: map[string]any{"department": "pay"}}
	s := newBrokerTestServer(t, enf, stubVerifier{claims: claims}, br, &captureSink{})

	body, _ := json.Marshal(map[string]any{
		"id_token": "x", "cluster": "prod", "namespace": "n", "pod": "p", "mode": []string{"egress"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/authorize", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleInterceptAuthorize(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]any{"department": "pay"}, br.lastReq.Claims, "full claims must be forwarded to the broker")
	require.Equal(t, "alice@corp", br.lastReq.Actor)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "sess-1", resp["session_id"])
	require.Equal(t, "tok", resp["token"])
}

func TestInterceptAuthorize_DeniedIdentity403(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", interceptIdentityPolicy)
	require.NoError(t, err)

	sink := &captureSink{}
	br := &stubBroker{}
	// groups that map to no identity → policy denies.
	claims := oidc.Claims{Subject: "bob", Email: "bob@corp", Groups: []string{"other"}}
	s := newBrokerTestServer(t, enf, stubVerifier{claims: claims}, br, sink)

	body, _ := json.Marshal(map[string]any{
		"id_token": "x", "cluster": "prod", "namespace": "n", "pod": "p", "mode": []string{"egress"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/authorize", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleInterceptAuthorize(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.True(t, hasAuditEvent(sink, "intercept_authorize", "bob@corp", "deny"), "expected intercept_authorize deny audit event")
}

func TestInterceptStop_Success204(t *testing.T) {
	br := &stubBroker{}
	s := newBrokerTestServer(t, nil, stubVerifier{claims: oidc.Claims{Subject: "alice", Email: "alice@corp"}}, br, &captureSink{})

	body, _ := json.Marshal(map[string]any{"id_token": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/sess-1/stop", bytes.NewReader(body))
	req = withURLParam(req, "sess-1")
	rec := httptest.NewRecorder()
	s.handleInterceptStop(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
}

// TestInterceptStop_UsesResolvedIdentityActor proves that when the identity
// policy resolves successfully, handleInterceptStop passes the RESOLVED
// identity's User to Broker.Stop — not the raw claims.Email fallback (that
// fallback only applies when resolution itself errors; see
// TestInterceptStop_Success204, which has a nil enforcer) and not any
// client-supplied value (the request body carries no actor field at all). The
// claims email is deliberately different from the policy-resolved user so the
// assertion can't pass by coincidence.
func TestInterceptStop_UsesResolvedIdentityActor(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "id.rego", interceptIdentityPolicy)
	require.NoError(t, err)

	br := &stubBroker{}
	claims := oidc.Claims{Subject: "alice", Email: "alice-raw@corp", Groups: []string{"eng"}}
	s := newBrokerTestServer(t, enf, stubVerifier{claims: claims}, br, &captureSink{})

	body, _ := json.Marshal(map[string]any{"id_token": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/sess-1/stop", bytes.NewReader(body))
	req = withURLParam(req, "sess-1")
	rec := httptest.NewRecorder()
	s.handleInterceptStop(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "alice@corp", br.lastStopActor, "must be the policy-resolved identity, not claims.Email (alice-raw@corp)")
}

func TestInterceptStop_UnknownSession404(t *testing.T) {
	br := &stubBroker{stopErr: errors.New("intercept: unknown session")}
	s := newBrokerTestServer(t, nil, stubVerifier{claims: oidc.Claims{Subject: "alice", Email: "alice@corp"}}, br, &captureSink{})

	body, _ := json.Marshal(map[string]any{"id_token": "x"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/no-such-session/stop", bytes.NewReader(body))
	req = withURLParam(req, "no-such-session")
	rec := httptest.NewRecorder()
	s.handleInterceptStop(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestInterceptStop_ByToken204 proves that a stop request carrying the
// per-session token takes the StopByToken path: no id_token/nonce is
// required, and the handler forwards the id and token verbatim rather than
// resolving an actor.
func TestInterceptStop_ByToken204(t *testing.T) {
	br := &stubBroker{}
	// The verifier is a distraction here: presence of "token" in the request
	// body must short-circuit straight to StopByToken without ever calling
	// Verify. If the handler mistakenly fell through to the id_token/actor
	// path instead, br.Stop (not StopByToken) would be invoked and the
	// lastStopByToken* assertions below would fail.
	s := newBrokerTestServer(t, nil, stubVerifier{}, br, &captureSink{})

	body, _ := json.Marshal(map[string]any{"token": "sess-1-token"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/sess-1/stop", bytes.NewReader(body))
	req = withURLParam(req, "sess-1")
	rec := httptest.NewRecorder()
	s.handleInterceptStop(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "sess-1", br.lastStopByTokenID)
	require.Equal(t, "sess-1-token", br.lastStopByTokenToken)
}

// TestInterceptStop_ByTokenInvalid404 proves that a StopByToken failure
// (unknown session or invalid token) maps to a generic 404, mirroring the
// id_token/actor fallback's error handling.
func TestInterceptStop_ByTokenInvalid404(t *testing.T) {
	br := &stubBroker{stopByTokenErr: errors.New("intercept: invalid session token")}
	s := newBrokerTestServer(t, nil, stubVerifier{}, br, &captureSink{})

	body, _ := json.Marshal(map[string]any{"token": "wrong-token"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/sess-1/stop", bytes.NewReader(body))
	req = withURLParam(req, "sess-1")
	rec := httptest.NewRecorder()
	s.handleInterceptStop(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestInterceptStop_TokenNeverLogged proves that a stop request carrying the
// per-session token never appears in the log output, whether the request
// succeeds or the broker rejects it.
func TestInterceptStop_TokenNeverLogged(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	const secretToken = "sess-1-super-secret-token"
	br := &stubBroker{stopByTokenErr: errors.New("intercept: invalid session token")}
	s := newBrokerTestServer(t, nil, stubVerifier{}, br, &captureSink{})

	body, _ := json.Marshal(map[string]any{"token": secretToken})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/sess-1/stop", bytes.NewReader(body))
	req = withURLParam(req, "sess-1")
	rec := httptest.NewRecorder()
	s.handleInterceptStop(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	for _, entry := range logs.All() {
		require.NotContains(t, entry.Message, secretToken)
		for _, f := range entry.Context {
			require.NotContains(t, f.String, secretToken)
		}
	}
	require.False(t, strings.Contains(rec.Body.String(), secretToken), "response body must not echo the token back")
}

func TestInterceptConfig_ReportsEnabledAndDefaultMode(t *testing.T) {
	s := &Server{
		opts:            Options{InterceptDefaultMode: []string{"egress"}},
		interceptBroker: &stubBroker{},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/intercept/config", nil)
	rec := httptest.NewRecorder()
	s.handleInterceptConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Enabled     bool     `json:"enabled"`
		DefaultMode []string `json:"default_mode"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.True(t, out.Enabled)
	require.Equal(t, []string{"egress"}, out.DefaultMode)
}

func TestInterceptConfig_DisabledWhenNoBroker(t *testing.T) {
	s := &Server{opts: Options{}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/intercept/config", nil)
	rec := httptest.NewRecorder()
	s.handleInterceptConfig(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Enabled bool `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.False(t, out.Enabled)
}
