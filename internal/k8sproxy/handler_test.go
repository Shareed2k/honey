package k8sproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/policy"
)

// testCA is a throwaway CA used to sign both the client certificate presented by
// the test https client and (as the ClientCAs trust anchor) verify it.
type testCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{
		cert:    cert,
		key:     key,
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
	}
}

// clientCert issues a client certificate with CommonName "alice" and no groups.
func (ca *testCA) clientCert(t *testing.T) tls.Certificate {
	t.Helper()
	return ca.clientCertWithGroups(t, "alice", nil)
}

// clientCertWithGroups issues a client certificate with the given CommonName and
// groups (recorded in Subject Organization, O=).
func (ca *testCA) clientCertWithGroups(t *testing.T, cn string, groups []string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn, Organization: groups},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// captureSink records audit events for assertions.
type captureSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *captureSink) Log(_ context.Context, e audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}
func (s *captureSink) Close() error { return nil }
func (s *captureSink) snapshot() []audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]audit.Event, len(s.events))
	copy(out, s.events)
	return out
}

// mtlsHarness wires a fake upstream API server, the proxy handler under an mTLS
// httptest server, and an https client presenting a client cert signed by the
// same test CA the server trusts.
type mtlsHarness struct {
	upstream *httptest.Server
	front    *httptest.Server
	ca       *testCA
	sink     *captureSink
}

func (h *mtlsHarness) close() {
	h.front.Close()
	h.upstream.Close()
}

func newMTLSHarness(t *testing.T, enf *policy.Enforcer) *mtlsHarness {
	return newMTLSHarnessSpec(t, enf, []string{"developers"}, nil)
}

// newMTLSHarnessSpec is newMTLSHarness with the cluster's DefaultGroups and
// Labels configurable (for the groups + cluster_labels gate tests).
func newMTLSHarnessSpec(t *testing.T, enf *policy.Enforcer, defaultGroups []string, labels map[string]string) *mtlsHarness {
	t.Helper()

	upstream := newEchoServer(t)

	reg, err := NewRegistry([]ClusterSpec{
		{Name: "prod", Config: &rest.Config{Host: upstream.URL}, DefaultGroups: defaultGroups, Labels: labels},
	})
	require.NoError(t, err)

	ca := newTestCA(t)
	sink := &captureSink{}
	h := NewHandler(reg, enf, sink)

	front := httptest.NewUnstartedServer(h)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(ca.certPEM))
	front.TLS = &tls.Config{
		ClientCAs:  pool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
	}
	front.StartTLS()

	return &mtlsHarness{upstream: upstream, front: front, ca: ca, sink: sink}
}

// client returns an https client presenting cert and trusting the front server.
func (h *mtlsHarness) client(t *testing.T, cert *tls.Certificate) *http.Client {
	t.Helper()
	serverCertPool := x509.NewCertPool()
	serverCertPool.AddCert(h.front.Certificate())
	tlsCfg := &tls.Config{
		RootCAs:    serverCertPool,
		MinVersion: tls.VersionTLS12,
	}
	if cert != nil {
		tlsCfg.Certificates = []tls.Certificate{*cert}
	}
	transport := &http.Transport{
		TLSClientConfig:   tlsCfg,
		DisableKeepAlives: true,
	}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

func TestHandler_ForwardsWithImpersonation(t *testing.T) {
	h := newMTLSHarness(t, nil)
	defer h.close()

	cert := h.ca.clientCert(t)
	resp, err := h.client(t, &cert).Get(h.front.URL + "/prod/api/v1/namespaces/default/pods")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusTeapot, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var echo echoResponse
	require.NoError(t, json.Unmarshal(body, &echo))

	// The /prod prefix is stripped before forwarding.
	require.Equal(t, "/api/v1/namespaces/default/pods", echo.Path)
	// honey sets its own impersonation from the client-cert CN + cluster groups.
	require.Equal(t, []string{"alice"}, echo.Headers["Impersonate-User"])
	require.Equal(t, []string{"developers"}, echo.Headers["Impersonate-Group"])

	// An allow audit event is emitted with the parsed request shape.
	events := h.sink.snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "allow", events[0].Decision)
	require.Equal(t, "k8s_request", events[0].Action)
	require.Equal(t, "alice", events[0].Actor)
	require.Equal(t, "prod", events[0].Target)
	require.Equal(t, "list", events[0].Extra["verb"])
	require.Equal(t, "pods", events[0].Extra["resource"])
	require.Equal(t, "default", events[0].Extra["namespace"])
}

func TestHandler_NoClientCertRejected(t *testing.T) {
	h := newMTLSHarness(t, nil)
	defer h.close()

	// A client without a certificate must be rejected by the TLS handshake
	// (RequireAndVerifyClientCert), never reaching the handler.
	_, err := h.client(t, nil).Get(h.front.URL + "/prod/api/v1/namespaces/default/pods")
	require.Error(t, err)

	require.Empty(t, h.sink.snapshot(), "rejected handshake must not audit")
}

func TestHandler_UnknownClusterReturns404(t *testing.T) {
	h := newMTLSHarness(t, nil)
	defer h.close()

	cert := h.ca.clientCert(t)
	resp, err := h.client(t, &cert).Get(h.front.URL + "/nope/api/v1/namespaces/default/pods")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Empty(t, h.sink.snapshot())
}

func TestHandler_PolicyDenyReturns403AndAudits(t *testing.T) {
	enf := denyEnforcer(t)
	h := newMTLSHarness(t, enf)
	defer h.close()

	cert := h.ca.clientCert(t)
	resp, err := h.client(t, &cert).Get(h.front.URL + "/prod/api/v1/namespaces/default/pods")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	events := h.sink.snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "deny", events[0].Decision)
	require.Equal(t, "alice", events[0].Actor)
	require.Equal(t, "prod", events[0].Target)
}

func TestHandler_PolicyAllowAudits(t *testing.T) {
	enf := allowEnforcer(t)
	h := newMTLSHarness(t, enf)
	defer h.close()

	cert := h.ca.clientCert(t)
	resp, err := h.client(t, &cert).Get(h.front.URL + "/prod/api/v1/namespaces/default/pods")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusTeapot, resp.StatusCode)

	events := h.sink.snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "allow", events[0].Decision)
}

func denyEnforcer(t *testing.T) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(t.Context(), "deny.rego", `package honey
import rego.v1
default allow := false
default deny_reason := "nope"
`)
	require.NoError(t, err)
	return enf
}

func allowEnforcer(t *testing.T) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(t.Context(), "allow.rego", `package honey
import rego.v1
default allow := true
default deny_reason := ""
`)
	require.NoError(t, err)
	return enf
}

// TestHandler_GroupsAndClusterLabelsInPolicy proves the k8s_request gate sees
// the client cert's groups (O=) and the cluster's labels: a policy requiring
// both admits an O=developers cert against an env:dev cluster and denies a cert
// with no groups (no DefaultGroups fallback configured here).
func TestHandler_GroupsAndClusterLabelsInPolicy(t *testing.T) {
	enf, err := policy.NewFromSource(t.Context(), "p.rego", `package honey
import rego.v1
default allow := false
default deny_reason := "denied"
allow if {
	input.action == "k8s_request"
	"developers" in input.groups
	input.cluster_labels.env == "dev"
}`)
	require.NoError(t, err)

	h := newMTLSHarnessSpec(t, enf, nil, map[string]string{"env": "dev"})
	defer h.close()

	// O=developers against env:dev → allowed (echo upstream returns 418).
	cert := h.ca.clientCertWithGroups(t, "alice", []string{"developers"})
	resp, err := h.client(t, &cert).Get(h.front.URL + "/prod/api/v1/namespaces/default/pods")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusTeapot, resp.StatusCode)

	// No groups (and no DefaultGroups fallback) → forbidden by policy.
	certNoGrp := h.ca.clientCert(t)
	resp2, err := h.client(t, &certNoGrp).Get(h.front.URL + "/prod/api/v1/namespaces/default/pods")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusForbidden, resp2.StatusCode)
}
