//go:build integration

package integration

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/webserver"
)

// webhookOPAServer wires app "webhook_app" to a sync recipe that writes $WEBHOOK_MSG
// into outPath on the SSH target, under the supplied OPA enforcer.
func webhookOPAServer(t *testing.T, target sshTarget, outPath string, enf *policy.Enforcer) string {
	t.Helper()
	tmpDir := t.TempDir()
	recipePath := filepath.Join(tmpDir, "webhook.cue")
	cue := `
recipe: {
	name: "opa-webhook"
	webhooks: {
		"hook1": {
			extract: { "WEBHOOK_MSG": "payload.message" }
			idempotency_key: "payload.message.id"
			async: false
		}
	}
	steps: [
		{
			host: "*"
			env: { WEBHOOK_MSG: string | *"" }
			command: "echo $WEBHOOK_MSG > ` + outPath + `"
		}
	]
}
`
	require.NoError(t, os.WriteFile(recipePath, []byte(cue), 0o600))

	configPath := filepath.Join(tmpDir, "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	return newTestServer(t, webserver.Options{
		ConfigPath:     configPath,
		Token:          "test-token",
		SearchRegistry: target.searchReg,
		ExecRegistry:   target.execReg,
		Enforcer:       enf,
		Config: &config.File{
			Apps: map[string]apps.AppConfig{
				"webhook_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"},
			},
			Defaults: config.Defaults{SSHUser: "testuser"},
		},
	})
}

// webhookActorServer wires a webhook whose OPA actor is extracted from the
// payload (sender.login). The command echoes that login into outPath.
func webhookActorServer(t *testing.T, target sshTarget, outPath string, enf *policy.Enforcer) string {
	t.Helper()
	tmpDir := t.TempDir()
	recipePath := filepath.Join(tmpDir, "webhook.cue")
	cue := `
recipe: {
	name: "opa-webhook-actor"
	webhooks: {
		"hook1": {
			actor: "sender.login"
			extract: { "WHO": "sender.login" }
			idempotency_key: "sender.login"
			async: false
		}
	}
	steps: [
		{
			host: "*"
			env: { WHO: string | *"" }
			command: "echo $WHO > ` + outPath + `"
		}
	]
}
`
	require.NoError(t, os.WriteFile(recipePath, []byte(cue), 0o600))
	configPath := filepath.Join(tmpDir, "honey.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	return newTestServer(t, webserver.Options{
		ConfigPath:     configPath,
		Token:          "test-token",
		SearchRegistry: target.searchReg,
		ExecRegistry:   target.execReg,
		Enforcer:       enf,
		Config: &config.File{
			Apps: map[string]apps.AppConfig{
				"webhook_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"},
			},
			Defaults: config.Defaults{SSHUser: "testuser"},
		},
	})
}

func senderPayload(login string) map[string]any {
	return map[string]any{"sender": map[string]string{"login": login}}
}

func TestOPAE2E_Webhook_ActorFromPayload(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 30 * time.Second}
	// Admission allows only actor "octocat" (extracted from sender.login).
	enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "recipe_execute"
	input.actor != "octocat"
}
deny_reason := "actor not permitted" if {
	input.action == "recipe_execute"
	input.actor != "octocat"
}`)

	t.Run("permitted payload actor runs", func(t *testing.T) {
		out := "/tmp/opa_webhook_actor_allow.txt"
		base := webhookActorServer(t, target, out, enf)
		resp := doJSON(t, client, base+"/api/v1/webhooks/webhook_app/hook1", senderPayload("octocat"))
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		time.Sleep(time.Second)
		got, rerr := target.readFile(t, out)
		require.NoError(t, rerr)
		assert.Contains(t, got, "octocat")
	})

	t.Run("other payload actor denied", func(t *testing.T) {
		out := "/tmp/opa_webhook_actor_deny.txt"
		base := webhookActorServer(t, target, out, enf)
		resp := doJSON(t, client, base+"/api/v1/webhooks/webhook_app/hook1", senderPayload("mallory"))
		defer resp.Body.Close()
		require.GreaterOrEqual(t, resp.StatusCode, 400)

		if _, rerr := target.readFile(t, out); rerr == nil {
			t.Fatal("denied actor must not create the output file")
		}
	})
}

func webhookPayload(id, text string) map[string]any {
	return map[string]any{
		"payload": map[string]any{
			"message": map[string]string{"id": id, "text": text},
		},
	}
}

func TestOPAE2E_Webhook_Admission(t *testing.T) {
	target := newSSHTarget(t)
	client := &http.Client{Timeout: 30 * time.Second}

	t.Run("admission allows webhook run", func(t *testing.T) {
		out := "/tmp/opa_webhook_allow.txt"
		allowAll, err := policy.New(context.Background(), "", nil) // embedded default = allow
		require.NoError(t, err)
		base := webhookOPAServer(t, target, out, allowAll)

		resp := doJSON(t, client, base+"/api/v1/webhooks/webhook_app/hook1", webhookPayload("allow-1", "permitted"))
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		time.Sleep(time.Second)
		got, rerr := target.readFile(t, out)
		require.NoError(t, rerr)
		assert.Contains(t, got, "allow-1")
	})

	t.Run("admission denies webhook run", func(t *testing.T) {
		out := "/tmp/opa_webhook_deny.txt"
		// Deny exactly the webhook actor; everything else allowed.
		enf := newEnforcer(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if input.actor == "webhook:webhook_app"
deny_reason := "webhook actor blocked" if input.actor == "webhook:webhook_app"`)
		base := webhookOPAServer(t, target, out, enf)

		resp := doJSON(t, client, base+"/api/v1/webhooks/webhook_app/hook1", webhookPayload("deny-1", "blocked"))
		defer resp.Body.Close()
		require.GreaterOrEqual(t, resp.StatusCode, 400, "denied webhook run should not return 2xx")

		if _, rerr := target.readFile(t, out); rerr == nil {
			t.Fatal("denied webhook must not create the output file")
		}
	})
}
