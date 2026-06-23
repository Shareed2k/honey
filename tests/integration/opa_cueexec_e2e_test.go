//go:build integration

package integration

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/webserver"
)

// writeCmdRecipe writes a one-step command recipe that echoes $MARK into outPath
// on the target host. Returns the absolute recipe path.
func writeCmdRecipe(t *testing.T, name, outPath string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name+".cue")
	content := fmt.Sprintf(`
recipe: {
	name: %q
	steps: [
		{
			host: "*"
			env: { MARK: string | *"" }
			command: "echo $MARK > %s"
		}
	]
}
`, name, outPath)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

// cueExecServer registers recipePath as an app (so it passes the recipe-path
// allowlist) and boots a server with the supplied auth/policy options.
func cueExecServer(t *testing.T, target sshTarget, recipePath string, opts webserver.Options) string {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))
	opts.Config = &config.File{
		Apps: map[string]apps.AppConfig{
			"opa_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"},
		},
		Defaults: config.Defaults{SSHUser: "testuser"},
	}
	opts.ConfigPath = configPath
	opts.SearchRegistry = target.searchReg
	opts.ExecRegistry = target.execReg
	return newTestServer(t, opts)
}

func postCueExec(t *testing.T, client *http.Client, baseURL, recipePath string, recs []hosts.Record, env []string, headers map[string]string) *http.Response {
	t.Helper()
	body := webserver.CueExecRequest{
		RecipePath: recipePath,
		Execute:    true,
		SSHUser:    "testuser",
		Records:    recs,
		Env:        env,
	}
	return doJSONHeaders(t, client, http.MethodPost, baseURL+"/api/v1/cue-exec", body, headers)
}

func decodeResults(t *testing.T, resp *http.Response) []map[string]any {
	t.Helper()
	var out struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out.Results
}

// --- Task 2: JWT identity + admission ------------------------------------

func TestOPAE2E_CueExec_JWTAdmission(t *testing.T) {
	target := newSSHTarget(t)
	pub, priv, _ := ed25519.GenerateKey(nil)
	// One enforcer gates api_request (middleware), recipe_execute (admission)
	// AND step_execute (host filter). Default-allow so API + host steps pass;
	// discriminate only on recipe_execute admission by actor.
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
	client := &http.Client{Timeout: 30 * time.Second}

	t.Run("allowed subject runs recipe", func(t *testing.T) {
		out := "/tmp/opa_jwt_allow.txt"
		recipePath := writeCmdRecipe(t, "opa-jwt-allow", out)
		base := cueExecServer(t, target, recipePath, webserver.Options{DisableAuth: true, JWTPubKey: pub, Enforcer: enf})

		resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=allowed"},
			map[string]string{"Authorization": "Bearer " + signJWT(t, priv, "alice")})
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		time.Sleep(time.Second)
		got, err := target.readFile(t, out)
		require.NoError(t, err)
		assert.Contains(t, got, "allowed")
	})

	t.Run("denied subject blocked pre-flight", func(t *testing.T) {
		out := "/tmp/opa_jwt_deny.txt"
		recipePath := writeCmdRecipe(t, "opa-jwt-deny", out)
		base := cueExecServer(t, target, recipePath, webserver.Options{DisableAuth: true, JWTPubKey: pub, Enforcer: enf})

		resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=denied"},
			map[string]string{"Authorization": "Bearer " + signJWT(t, priv, "bob")})
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		if _, err := target.readFile(t, out); err == nil {
			t.Fatal("denied run must not create the output file")
		}
	})
}

// --- Task 3: trusted proxy header identity -------------------------------

func TestOPAE2E_CueExec_ProxyHeaderAdmission(t *testing.T) {
	target := newSSHTarget(t)
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "recipe_execute"
	input.actor != "ops"
}
deny_reason := "actor not permitted" if {
	input.action == "recipe_execute"
	input.actor != "ops"
}`)
	client := &http.Client{Timeout: 30 * time.Second}
	trustedNets := mustCIDRs(t, "127.0.0.0/8")

	t.Run("trusted ops header runs", func(t *testing.T) {
		out := "/tmp/opa_proxy_allow.txt"
		recipePath := writeCmdRecipe(t, "opa-proxy-allow", out)
		base := cueExecServer(t, target, recipePath, webserver.Options{Token: "test-token", TrustedProxyNets: trustedNets, Enforcer: enf})

		resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=allowed"},
			map[string]string{"Authorization": authHeader(), "X-Honey-User": "ops"})
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		time.Sleep(time.Second)
		got, err := target.readFile(t, out)
		require.NoError(t, err)
		assert.Contains(t, got, "allowed")
	})

	t.Run("other user denied", func(t *testing.T) {
		out := "/tmp/opa_proxy_deny.txt"
		recipePath := writeCmdRecipe(t, "opa-proxy-deny", out)
		base := cueExecServer(t, target, recipePath, webserver.Options{Token: "test-token", TrustedProxyNets: trustedNets, Enforcer: enf})

		resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=denied"},
			map[string]string{"Authorization": authHeader(), "X-Honey-User": "intruder"})
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)

		if _, err := target.readFile(t, out); err == nil {
			t.Fatal("denied run must not create the output file")
		}
	})
}

// --- Task 4: host-list filtering -----------------------------------------

func TestOPAE2E_CueExec_HostFilter(t *testing.T) {
	target := newSSHTarget(t)
	// Admission allowed; the ssh-test host is denied at step_execute.
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "step_execute"
	input.host == "ssh-test"
}
deny_reason := "host blocked" if {
	input.action == "step_execute"
	input.host == "ssh-test"
}`)
	client := &http.Client{Timeout: 30 * time.Second}

	out := "/tmp/opa_hostfilter.txt"
	recipePath := writeCmdRecipe(t, "opa-hostfilter", out)
	base := cueExecServer(t, target, recipePath, webserver.Options{Token: "test-token", Enforcer: enf})

	resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=ran"},
		map[string]string{"Authorization": authHeader()})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	results := decodeResults(t, resp)
	var sawSkip bool
	for _, r := range results {
		if skipped, _ := r["Skipped"].(bool); skipped {
			sawSkip = true
		}
	}
	assert.True(t, sawSkip, "expected a skipped result for the policy-denied host: %v", results)

	if _, err := target.readFile(t, out); err == nil {
		t.Fatal("filtered host must not run the command")
	}
}

// --- inventory host_vars driving the host filter --------------------------

func TestOPAE2E_CueExec_InventoryHostVar(t *testing.T) {
	target := newSSHTarget(t)
	// Deny step_execute on any host whose resolved inventory var blocked == true.
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "step_execute"
	input.host_vars.blocked == true
}
deny_reason := "host blocked by inventory" if {
	input.action == "step_execute"
	input.host_vars.blocked == true
}`)
	client := &http.Client{Timeout: 30 * time.Second}

	out := "/tmp/opa_inv_hostvar.txt"
	recipePath := writeCmdRecipe(t, "opa-inv-hostvar", out)

	configPath := filepath.Join(t.TempDir(), "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))
	base := newTestServer(t, webserver.Options{
		Token:          "test-token",
		Enforcer:       enf,
		ConfigPath:     configPath,
		SearchRegistry: target.searchReg,
		ExecRegistry:   target.execReg,
		Config: &config.File{
			Apps: map[string]apps.AppConfig{
				"opa_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"},
			},
			Defaults: config.Defaults{SSHUser: "testuser"},
			Inventory: config.Inventory{
				Hosts: map[string]config.InventoryHost{
					"ssh-test": {Vars: map[string]config.InventoryValue{"blocked": config.MustInventoryValue(true)}},
				},
			},
		},
	})

	resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=ran"},
		map[string]string{"Authorization": authHeader()})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	results := decodeResults(t, resp)
	var sawSkip bool
	for _, r := range results {
		if skipped, _ := r["Skipped"].(bool); skipped {
			sawSkip = true
		}
	}
	assert.True(t, sawSkip, "host blocked by inventory var should be skipped: %v", results)

	if _, err := target.readFile(t, out); err == nil {
		t.Fatal("inventory-blocked host must not run the command")
	}
}

// --- Task 5: opa step (allow & deny) -------------------------------------

// writeOPAStepRecipe writes a recipe (opa step + command step) and its policy
// into one dir, returning the recipe path. The command writes to outPath.
func writeOPAStepRecipe(t *testing.T, name, outPath, policyRego string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gate.rego"), []byte(policyRego), 0o600))
	p := filepath.Join(dir, name+".cue")
	content := fmt.Sprintf(`
recipe: {
	name: %q
	steps: [
		{ host: "_", opa: { policy: "gate.rego" } },
		{ host: "*", command: "echo ok > %s" },
	]
}
`, name, outPath)
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestOPAE2E_OPAStep(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 30 * time.Second}

	t.Run("allow runs the command step", func(t *testing.T) {
		out := "/tmp/opa_step_allow.txt"
		recipePath := writeOPAStepRecipe(t, "opa-step-allow", out, `package honey
import rego.v1
default allow := true
`)
		base := cueExecServer(t, target, recipePath, webserver.Options{Token: "test-token"})
		resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, nil,
			map[string]string{"Authorization": authHeader()})
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		time.Sleep(time.Second)
		got, err := target.readFile(t, out)
		require.NoError(t, err)
		assert.Contains(t, got, "ok")
	})

	t.Run("deny fails the step and skips the command", func(t *testing.T) {
		out := "/tmp/opa_step_deny.txt"
		recipePath := writeOPAStepRecipe(t, "opa-step-deny", out, `package honey
import rego.v1
default allow := false
default deny_reason := "step blocked by policy"
`)
		base := cueExecServer(t, target, recipePath, webserver.Options{Token: "test-token"})
		resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, nil,
			map[string]string{"Authorization": authHeader()})
		defer resp.Body.Close()
		// opa-step failure surfaces mid-stream (not pre-flight), so the request
		// still returns 200 with a failed result.
		require.Equal(t, http.StatusOK, resp.StatusCode)

		results := decodeResults(t, resp)
		var sawFailure bool
		for _, r := range results {
			if success, _ := r["Success"].(bool); !success {
				sawFailure = true
			}
		}
		assert.True(t, sawFailure, "expected a failed result from the denied opa step: %v", results)

		if _, err := target.readFile(t, out); err == nil {
			t.Fatal("denied opa step must prevent the command step")
		}
	})
}

// --- Task 7: API gate over real HTTP -------------------------------------

func TestOPAE2E_APIGate(t *testing.T) {
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if input.path == "/api/v1/providers"
deny_reason := "providers endpoint blocked" if input.path == "/api/v1/providers"`)
	target := newSSHTarget(t)
	base := cueExecServer(t, target, writeCmdRecipe(t, "noop", "/tmp/noop.txt"),
		webserver.Options{Token: "test-token", Enforcer: enf})
	client := &http.Client{Timeout: 15 * time.Second}

	t.Run("denied path returns 403", func(t *testing.T) {
		resp := doJSONHeaders(t, client, http.MethodGet, base+"/api/v1/providers", nil,
			map[string]string{"Authorization": authHeader()})
		defer resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("allowed path not blocked", func(t *testing.T) {
		resp := doJSONHeaders(t, client, http.MethodGet, base+"/api/v1/recipes", nil,
			map[string]string{"Authorization": authHeader()})
		defer resp.Body.Close()
		require.NotEqual(t, http.StatusForbidden, resp.StatusCode)
	})
}
