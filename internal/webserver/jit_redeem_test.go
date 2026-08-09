package webserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/jit"
	"github.com/shareed2k/honey/internal/sshca"
)

// newJITRedeemTestServer builds a *Server the same way newJitTestServer does
// (a jit.Store backed by a temp file) but additionally overrides the SSH CA
// NewServer wired from the real host state dir with one rooted at
// t.TempDir(), so cert-minting tests are isolated and don't depend on (or
// pollute) the machine's actual honey state dir.
func newJITRedeemTestServer(t *testing.T) (*Server, *jit.Store) {
	t.Helper()
	s, store := newJitTestServer(t, Options{})
	ca, err := sshca.LoadOrCreateCA(t.TempDir())
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	s.sshCA = ca
	return s, store
}

func TestHandleJITRedeemStatus_CertGrant(t *testing.T) {
	s, store := newJITRedeemTestServer(t)
	_, code, err := store.Create(jit.Grant{
		Actor:        "alice",
		Recipient:    "bob",
		Resource:     jit.ResourceRef{Name: "host1", Provider: "docker"},
		Capabilities: []jit.Capability{jit.CapShell},
		Delivery:     jit.DeliveryCert,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	w := doJSON(t, s, http.MethodGet, "/api/v1/jit/redeem/"+code, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var resp struct {
		Status string `json:"status"`
		Active bool   `json:"active"`
		Offers struct {
			Web  bool `json:"web"`
			Cert bool `json:"cert"`
		} `json:"offers"`
		Resource struct {
			Name     string `json:"name"`
			Provider string `json:"provider"`
		} `json:"resource"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != string(jit.StatusApproved) {
		t.Fatalf("status = %q, want approved", resp.Status)
	}
	if !resp.Active {
		t.Fatal("expected active = true")
	}
	if !resp.Offers.Cert {
		t.Fatal("expected offers.cert = true for a cert-delivery shell grant")
	}
	if resp.Offers.Web {
		t.Fatal("expected offers.web = false for a cert-only delivery grant")
	}
	if resp.Resource.Name != "host1" || resp.Resource.Provider != "docker" {
		t.Fatalf("unexpected resource in response: %+v", resp.Resource)
	}

	// The redeem code and its hash must never appear in the response body.
	raw := w.Body.String()
	if strings.Contains(raw, code) || strings.Contains(raw, "code_hash") {
		t.Fatalf("status response leaks the code or its hash: %s", raw)
	}
}

func TestHandleJITRedeemStatus_WebOnlyGrant(t *testing.T) {
	s, store := newJITRedeemTestServer(t)
	_, code, err := store.Create(jit.Grant{
		Actor:        "alice",
		Resource:     jit.ResourceRef{Name: "host1"},
		Capabilities: []jit.Capability{jit.CapShell},
		Delivery:     jit.DeliveryWeb,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	w := doJSON(t, s, http.MethodGet, "/api/v1/jit/redeem/"+code, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var resp struct {
		Offers struct {
			Web  bool `json:"web"`
			Cert bool `json:"cert"`
		} `json:"offers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Offers.Web {
		t.Fatal("expected offers.web = true for a web-delivery shell grant")
	}
	if resp.Offers.Cert {
		t.Fatal("expected offers.cert = false for a web-only delivery grant")
	}
}

func TestHandleJITRedeemStatus_UnknownCode(t *testing.T) {
	s, _ := newJITRedeemTestServer(t)
	w := doJSON(t, s, http.MethodGet, "/api/v1/jit/redeem/this-code-does-not-exist", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body)
	}
}

func TestHandleJITRedeemCert_HappyPathAndRedemptionCap(t *testing.T) {
	s, store := newJITRedeemTestServer(t)
	duration := time.Hour
	_, code, err := store.Create(jit.Grant{
		Actor:          "alice",
		Recipient:      "bob",
		Resource:       jit.ResourceRef{Name: "host1"},
		Capabilities:   []jit.Capability{jit.CapShell},
		Delivery:       jit.DeliveryCert,
		Duration:       duration,
		MaxRedemptions: 1,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	pubKey := newSSHPubKey(t)
	before := time.Now()
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": pubKey})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var resp struct {
		Cert            string   `json:"cert"`
		CA              string   `json:"ca"`
		Principals      []string `json:"principals"`
		ValidBeforeUnix uint64   `json:"valid_before_unix"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Principals) != 1 || resp.Principals[0] != "bob" {
		t.Fatalf("principals = %v, want [bob]", resp.Principals)
	}
	if resp.Cert == "" || resp.CA == "" {
		t.Fatal("expected non-empty cert and ca")
	}
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(resp.Cert))
	if err != nil {
		t.Fatalf("parse returned cert: %v", err)
	}
	cert, ok := parsed.(*ssh.Certificate)
	if !ok {
		t.Fatalf("parsed key is not a certificate: %T", parsed)
	}
	if len(cert.ValidPrincipals) != 1 || cert.ValidPrincipals[0] != "bob" {
		t.Fatalf("cert principals = %v, want [bob]", cert.ValidPrincipals)
	}
	wantValidBefore := before.Add(duration).Unix()
	gotValidBefore := int64(cert.ValidBefore)
	delta := gotValidBefore - wantValidBefore
	if delta < -5 || delta > 5 {
		t.Fatalf("cert.ValidBefore = %d, want ~%d (delta %d)", gotValidBefore, wantValidBefore, delta)
	}

	// The redeem code must never appear in the response body.
	if strings.Contains(w.Body.String(), code) {
		t.Fatalf("cert response leaks the code: %s", w.Body.String())
	}

	// A second redeem past MaxRedemptions=1 must collapse to a generic 404.
	w2 := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": pubKey})
	if w2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 on second redeem past cap, got %d body=%s", w2.Code, w2.Body)
	}
}

// A malformed public key is a client error (400) and must NOT consume a
// redemption — otherwise a typo would permanently kill a one-time link. After
// the bad request, a valid redeem on the same MaxRedemptions=1 grant still
// succeeds, proving no redemption was burned.
func TestHandleJITRedeemCert_BadKeyDoesNotConsumeRedemption(t *testing.T) {
	s, store := newJITRedeemTestServer(t)
	_, code, err := store.Create(jit.Grant{
		Actor:          "alice",
		Recipient:      "bob",
		Resource:       jit.ResourceRef{Name: "host1"},
		Capabilities:   []jit.Capability{jit.CapShell},
		Delivery:       jit.DeliveryCert,
		Duration:       time.Hour,
		MaxRedemptions: 1,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	bad := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": "not-a-valid-ssh-key"})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed public key, got %d body=%s", bad.Code, bad.Body)
	}

	// The single redemption must still be available.
	ok := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": newSSHPubKey(t)})
	if ok.Code != http.StatusOK {
		t.Fatalf("expected 200 on a valid redeem after the bad-key attempt (redemption must not have been consumed), got %d body=%s", ok.Code, ok.Body)
	}
}

func TestHandleJITRedeemCert_WebOnlyGrantRejected(t *testing.T) {
	s, store := newJITRedeemTestServer(t)
	_, code, err := store.Create(jit.Grant{
		Actor:        "alice",
		Recipient:    "bob",
		Resource:     jit.ResourceRef{Name: "host1"},
		Capabilities: []jit.Capability{jit.CapShell},
		Delivery:     jit.DeliveryWeb,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": newSSHPubKey(t)})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a web-only delivery grant, got %d body=%s", w.Code, w.Body)
	}
}

func TestJitRedeemInactiveReason(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	approved := func(mut func(*jit.Grant)) jit.Grant {
		g := jit.Grant{
			Status:    jit.StatusApproved,
			StartsAt:  now.Add(-time.Hour),
			ExpiresAt: now.Add(time.Hour),
		}
		mut(&g)
		return g
	}
	tests := []struct {
		name string
		g    jit.Grant
		want string
	}{
		{"pending", jit.Grant{Status: jit.StatusPending}, "pending"},
		{"denied", jit.Grant{Status: jit.StatusDenied}, "denied"},
		{"revoked", jit.Grant{Status: jit.StatusRevoked}, "revoked"},
		{"expired", approved(func(g *jit.Grant) { g.ExpiresAt = now.Add(-time.Minute) }), "expired"},
		{"not_started", approved(func(g *jit.Grant) { g.StartsAt = now.Add(time.Minute) }), "not_started"},
		{"exhausted", approved(func(g *jit.Grant) { g.MaxRedemptions = 1; g.Redemptions = 1 }), "exhausted"},
		{"inactive_fallback", approved(func(*jit.Grant) {}), "inactive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jitRedeemInactiveReason(tt.g, now); got != tt.want {
				t.Fatalf("jitRedeemInactiveReason = %q, want %q", got, tt.want)
			}
		})
	}
}

// A tunnel-capability grant is redeemable as an SSH certificate (used for
// `ssh -L` through the gateway), so a cert-delivery tunnel grant mints a cert.
func TestHandleJITRedeemCert_TunnelGrant(t *testing.T) {
	s, store := newJITRedeemTestServer(t)
	_, code, err := store.Create(jit.Grant{
		Actor:        "alice",
		Recipient:    "bob",
		Resource:     jit.ResourceRef{Name: "host1"},
		Capabilities: []jit.Capability{jit.CapTunnel},
		Delivery:     jit.DeliveryCert,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": newSSHPubKey(t)})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for a tunnel cert grant, got %d body=%s", w.Code, w.Body)
	}
}

func TestHandleJITRedeemCert_NilCA(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	s.sshCA = nil
	_, code, err := store.Create(jit.Grant{
		Actor:        "alice",
		Recipient:    "bob",
		Resource:     jit.ResourceRef{Name: "host1"},
		Capabilities: []jit.Capability{jit.CapShell},
		Delivery:     jit.DeliveryCert,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": newSSHPubKey(t)})
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with nil ssh ca, got %d body=%s", w.Code, w.Body)
	}
}

func TestHandleJITRedeemCert_PendingGrantRejected(t *testing.T) {
	s, store := newJITRedeemTestServer(t)
	_, code, err := store.Create(jit.Grant{
		Actor:           "alice",
		Recipient:       "bob",
		Resource:        jit.ResourceRef{Name: "host1"},
		Capabilities:    []jit.Capability{jit.CapShell},
		Delivery:        jit.DeliveryCert,
		Duration:        time.Hour,
		RequireApproval: true,
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/redeem/"+code+"/cert", map[string]any{"public_key": newSSHPubKey(t)})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a not-yet-approved grant, got %d body=%s", w.Code, w.Body)
	}
}
