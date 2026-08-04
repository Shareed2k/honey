package webserver

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

// probeActor wraps a handler that echoes the resolved actor, so identity tests
// can assert what authMiddleware put on the request context.
func probeActor(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(actorFromCtx(r.Context())))
}

func newTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if opts.Token == "" {
		opts.DisableAuth = true
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = "127.0.0.1:0"
	}
	s, err := NewServer(opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return s
}

func signEd25519JWT(t *testing.T, priv ed25519.PrivateKey, sub string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Subject:   sub,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return s
}

func TestAuthMiddleware_IdentityTrustedProxy(t *testing.T) {
	_, ipNet, _ := net.ParseCIDR("127.0.0.0/8")
	s := newTestServer(t, Options{TrustedProxyNets: []*net.IPNet{ipNet}})
	h := s.authMiddleware(http.HandlerFunc(probeActor))

	cases := []struct {
		name       string
		remoteAddr string
		header     string
		want       string
	}{
		{"trusted peer with header", "127.0.0.1:5555", "alice", "alice"},
		{"trusted peer no header", "127.0.0.1:5555", "", "api"},
		{"untrusted peer ignores header", "8.8.8.8:5555", "mallory", "api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/x", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.header != "" {
				req.Header.Set(trustedUserHeader, tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("code = %d, want 200", rec.Code)
			}
			if got := rec.Body.String(); got != tc.want {
				t.Fatalf("actor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAuthMiddleware_IdentityJWT(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	s := newTestServer(t, Options{JWTPubKey: pub})
	h := s.authMiddleware(http.HandlerFunc(probeActor))

	t.Run("valid jwt subject", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/x", nil)
		req.Header.Set("Authorization", "Bearer "+signEd25519JWT(t, priv, "bob"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Body.String(); got != "bob" {
			t.Fatalf("actor = %q, want bob", got)
		}
	})

	t.Run("wrong key rejected falls back to api", func(t *testing.T) {
		_, otherPriv, _ := ed25519.GenerateKey(nil)
		req := httptest.NewRequest("GET", "/api/v1/x", nil)
		req.Header.Set("Authorization", "Bearer "+signEd25519JWT(t, otherPriv, "bob"))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Body.String(); got != "api" {
			t.Fatalf("actor = %q, want api (bad signature)", got)
		}
	})
}

func TestParseEd25519PublicKey_RoundTrip(t *testing.T) {
	// Confirms the bootstrap key format (base64 std) matches what the server uses.
	pub, _, _ := ed25519.GenerateKey(nil)
	b64 := base64.StdEncoding.EncodeToString(pub)
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.PublicKey(decoded).Equal(pub) {
		t.Fatal("round-trip mismatch")
	}
}

func TestResolveWebhookActor(t *testing.T) {
	_, ipNet, _ := net.ParseCIDR("127.0.0.0/8")
	api := &RecipesAPI{opts: Options{TrustedProxyNets: []*net.IPNet{ipNet}}}
	body := []byte(`{"sender":{"login":"octocat"}}`)

	newReq := func(remote, userHeader string) *http.Request {
		r := httptest.NewRequest("POST", "/api/v1/webhooks/myapp/hook1", nil)
		r.RemoteAddr = remote
		if userHeader != "" {
			r.Header.Set(trustedUserHeader, userHeader)
		}
		return r
	}

	t.Run("trusted header wins over payload", func(t *testing.T) {
		got := api.resolveWebhookActor(newReq("127.0.0.1:9", "alice"),
			cuetry.RecipeWebhook{Actor: "sender.login"}, body, "myapp")
		if got != "alice" {
			t.Fatalf("actor = %q, want alice", got)
		}
	})

	t.Run("payload path when no trusted header", func(t *testing.T) {
		got := api.resolveWebhookActor(newReq("8.8.8.8:9", "mallory"), // untrusted peer → header ignored
			cuetry.RecipeWebhook{Actor: "sender.login"}, body, "myapp")
		if got != "octocat" {
			t.Fatalf("actor = %q, want octocat", got)
		}
	})

	t.Run("fallback to webhook:app", func(t *testing.T) {
		got := api.resolveWebhookActor(newReq("8.8.8.8:9", ""),
			cuetry.RecipeWebhook{}, body, "myapp")
		if got != "webhook:myapp" {
			t.Fatalf("actor = %q, want webhook:myapp", got)
		}
	})

	t.Run("fallback when payload path missing", func(t *testing.T) {
		got := api.resolveWebhookActor(newReq("8.8.8.8:9", ""),
			cuetry.RecipeWebhook{Actor: "does.not.exist"}, body, "myapp")
		if got != "webhook:myapp" {
			t.Fatalf("actor = %q, want webhook:myapp", got)
		}
	})
}

func TestGateInteractiveSession(t *testing.T) {
	const src = `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "interactive_session"
	input.target.env == "prod"
}
deny_reason := "no interactive shells on prod" if {
	input.action == "interactive_session"
	input.target.env == "prod"
}`
	enf, err := policy.NewFromSource(context.Background(), "s.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	s := newTestServer(t, Options{Enforcer: enf})
	req := httptest.NewRequest("GET", "/ws/ssh", nil)

	prod := hosts.Record{Name: "p1", Provider: "ssh", Meta: map[string]string{"env": "prod"}}
	if err := s.gateInteractiveSession(req, prod); err == nil {
		t.Fatal("prod interactive session should be denied")
	}
	stg := hosts.Record{Name: "s1", Provider: "ssh", Meta: map[string]string{"env": "staging"}}
	if err := s.gateInteractiveSession(req, stg); err != nil {
		t.Fatalf("staging session should be allowed: %v", err)
	}
}

func TestGateUDPRelay(t *testing.T) {
	const src = `package honey
import rego.v1
default allow := false
allow if {
	input.action == "udp_relay"
	input.target.host == "10.0.0.5"
}`
	enf, err := policy.NewFromSource(context.Background(), "udp.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	s := newTestServer(t, Options{Enforcer: enf})
	req := httptest.NewRequest("GET", "/api/v1/ws/udp", nil)

	if err := s.forwardingAPI.gateUDPRelay(req, "10.0.0.5:53"); err != nil {
		t.Fatalf("allowed target should pass: %v", err)
	}
	if err := s.forwardingAPI.gateUDPRelay(req, "1.2.3.4:53"); err == nil {
		t.Fatal("disallowed target should be denied")
	}
}

func TestGateTunnel(t *testing.T) {
	// Policy: deny every unix-socket target; allow only one tcp host.
	const src = `package honey
import rego.v1
default allow := false
allow if {
	input.action == "tunnel"
	input.target.scheme == "tcp"
	input.target.host == "10.0.0.5"
}`
	enf, err := policy.NewFromSource(context.Background(), "tunnel.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	s := newTestServer(t, Options{Enforcer: enf})
	req := httptest.NewRequest("GET", "/api/v1/ws/tunnel", nil)

	if err := s.forwardingAPI.gateTunnel(req, "10.0.0.5:5432"); err != nil {
		t.Fatalf("allowed tcp target should pass: %v", err)
	}
	if err := s.forwardingAPI.gateTunnel(req, "unix:/var/run/docker.sock"); err == nil {
		t.Fatal("unix target must be denied by policy")
	}
	if err := s.forwardingAPI.gateTunnel(req, "1.2.3.4:5432"); err == nil {
		t.Fatal("disallowed tcp target should be denied")
	}
}

func TestGateTunnel_nilEnforcerAllows(t *testing.T) {
	s := newTestServer(t, Options{})
	req := httptest.NewRequest("GET", "/api/v1/ws/tunnel", nil)
	if err := s.forwardingAPI.gateTunnel(req, "unix:/var/run/docker.sock"); err != nil {
		t.Fatalf("nil enforcer must allow: %v", err)
	}
}

func TestAuthMiddleware_OPAGate(t *testing.T) {
	// Policy: allow api_request only for actor "alice".
	const src = `package honey
import rego.v1
default allow := false
default deny_reason := "forbidden by policy"
allow if {
	input.action == "api_request"
	input.actor == "alice"
}`
	enf, err := policy.NewFromSource(context.Background(), "gate.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}

	_, ipNet, _ := net.ParseCIDR("127.0.0.0/8")
	s := newTestServer(t, Options{Enforcer: enf, TrustedProxyNets: []*net.IPNet{ipNet}})
	h := s.authMiddleware(http.HandlerFunc(probeActor))

	t.Run("allowed actor passes", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/x", nil)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set(trustedUserHeader, "alice")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", rec.Code)
		}
	})

	t.Run("denied actor gets 403", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/x", nil)
		req.RemoteAddr = "127.0.0.1:5555"
		req.Header.Set(trustedUserHeader, "mallory")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("code = %d, want 403", rec.Code)
		}
	})
}
