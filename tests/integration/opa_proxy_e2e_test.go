//go:build integration

package integration

import (
	"crypto/ed25519"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/webserver"
)

// TestOPAE2E_CaddyProxy runs honey behind a real Caddy reverse proxy and exercises
// the trusted-proxy identity path end to end:
//   - Caddy strips client X-Honey-User and re-derives it from X-Auth-User, so the
//     proxy is the identity authority; honey trusts Caddy's (RFC1918) source IP.
//   - authz uses the X-Honey-Token header, leaving Authorization free for JWT.
//
// Identity sources covered: trusted header injection, spoofing protection
// (direct loopback request is untrusted), and JWT-through-proxy.
func TestOPAE2E_CaddyProxy(t *testing.T) {
	target := newSSHTarget(t)
	pub, priv, _ := ed25519.GenerateKey(nil)

	// Admission allows only actor "alice"; api_request and step_execute default-allow.
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "recipe_execute"
	input.actor != "alice"
}
deny_reason := "actor not permitted" if {
	input.action == "recipe_execute"
	input.actor != "alice"
}`)

	// Recipe writes to a per-request file so subtests don't collide on the shared
	// SSH container: /tmp/opa_proxy_<MARK>.txt.
	dir := t.TempDir()
	recipePath := filepath.Join(dir, "proxy.cue")
	cue := `
recipe: {
	name: "opa-proxy"
	steps: [
		{
			host: "*"
			env: { MARK: string | *"" }
			command: "echo ran > /tmp/opa_proxy_${MARK}.txt"
		}
	]
}
`
	require.NoError(t, os.WriteFile(recipePath, []byte(cue), 0o600))
	configPath := filepath.Join(dir, "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	// honey on loopback; testcontainers forwards the host port to Caddy. The
	// forwarded connection's source IP is backend-dependent (loopback, an sshd
	// sidecar IP, or IPv6), so trust all peers here — the spoof defense under test
	// is Caddy's header-strip, not honey's IP allowlist (which the webserver unit
	// tests cover via TestAuthMiddleware_IdentityTrustedProxy).
	// DisableAuth: the JWT bearer and the shared-token check both want the
	// Authorization header (tokenFromRequest consumes the bearer first), so JWT
	// identity requires auth delegated to the proxy/JWT. Token-auth itself is
	// covered by the other cue-exec e2e tests.
	_, honeyPort := newTestServerOn(t, webserver.Options{
		DisableAuth:      true,
		JWTPubKey:        pub,
		TrustedProxyNets: mustCIDRs(t, "0.0.0.0/0", "::/0"),
		Enforcer:         enf,
		ConfigPath:       configPath,
		SearchRegistry:   target.searchReg,
		ExecRegistry:     target.execReg,
		Config: &config.File{
			Apps: map[string]apps.AppConfig{
				"opa_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"},
			},
			Defaults: config.Defaults{SSHUser: "testuser"},
		},
	}, "127.0.0.1")

	proxyBase := startCaddy(t, honeyPort)
	client := &http.Client{Timeout: 30 * time.Second}

	post := func(mark string, headers map[string]string) *http.Response {
		return postCueExec(t, client, proxyBase, recipePath, []hosts.Record{target.rec}, []string{"MARK=" + mark}, headers)
	}
	fileFor := func(mark string) string { return fmt.Sprintf("/tmp/opa_proxy_%s.txt", mark) }

	t.Run("proxy injects trusted user alice → allowed", func(t *testing.T) {
		resp := post("p_alice", map[string]string{"X-Auth-User": "alice"})
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		time.Sleep(time.Second)
		got, err := target.readFile(t, fileFor("p_alice"))
		require.NoError(t, err)
		require.Contains(t, got, "ran")
	})

	t.Run("proxy injects other user → denied", func(t *testing.T) {
		resp := post("p_bob", map[string]string{"X-Auth-User": "bob"})
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		if _, err := target.readFile(t, fileFor("p_bob")); err == nil {
			t.Fatal("denied actor must not run")
		}
	})

	t.Run("proxy strips client-supplied X-Honey-User (spoof defense)", func(t *testing.T) {
		// Client forges X-Honey-User: alice but provides no X-Auth-User. Caddy strips
		// the forged header and sets nothing → honey resolves actor "api" → denied.
		resp := post("p_spoof", map[string]string{"X-Honey-User": "alice"})
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		if _, err := target.readFile(t, fileFor("p_spoof")); err == nil {
			t.Fatal("spoofed header must not grant identity through the proxy")
		}
	})

	t.Run("JWT through proxy → allowed", func(t *testing.T) {
		// No X-Auth-User → Caddy leaves X-Honey-User empty → honey falls back to the
		// JWT bearer for identity. Authorization carries the JWT; X-Honey-Token authz.
		resp := post("p_jwt", map[string]string{"Authorization": "Bearer " + signJWT(t, priv, "alice")})
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		time.Sleep(time.Second)
		got, err := target.readFile(t, fileFor("p_jwt"))
		require.NoError(t, err)
		require.Contains(t, got, "ran")
	})
}
