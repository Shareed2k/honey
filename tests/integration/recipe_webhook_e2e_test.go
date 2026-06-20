//go:build integration

package integration

import (
	"context"
	"encoding/json"
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
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"
)

type webhookTestProvider struct {
	rec hosts.Record
}

func (p webhookTestProvider) ID() string            { return "test" }
func (p webhookTestProvider) BackendName() string   { return "test" }
func (p webhookTestProvider) CacheIdentity() string { return "test" }
func (p webhookTestProvider) Search(_ context.Context, _ hosts.Query) ([]hosts.Record, error) {
	return []hosts.Record{p.rec}, nil
}

type webhookTestFactory struct {
	rec hosts.Record
}

func (f webhookTestFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	return []hosts.Backend{webhookTestProvider{rec: f.rec}}
}
func (f webhookTestFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return webhookTestProvider{rec: f.rec}
}
func (f webhookTestFactory) BackendRows() []config.BackendRow { return nil }

func TestRecipeE2E_Webhook(t *testing.T) {
	// 1. Setup real SSH container
	sshH, sshP, keyFile := startSSH(t)

	// Build the target record for our test SSH container
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

	// 2. Setup mock search registry that returns our SSH container
	searchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{webhookTestFactory{rec: rec}})

	// 3. Setup mock exec registry that uses real SSH dialing to our container
	execReg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	// 4. Create the recipe file
	tmpDir := t.TempDir()
	recipePath := filepath.Join(tmpDir, "webhook.cue")
	cueContent := `
recipe: {
	name: "test-webhook"
	webhooks: {
		"hook1": {
			extract: {
				"WEBHOOK_MSG": "payload.message"
			}
			idempotency_key: "payload.message.id"
			async: false
		}
	}
	steps: [
		{
			host: "*"
			env: {
				WEBHOOK_MSG: string | *""
			}
			command: "echo \(env.WEBHOOK_MSG) > /tmp/webhook_out.txt"
		}
	]
}
`
	require.NoError(t, os.WriteFile(recipePath, []byte(cueContent), 0o600))

	// 5. Create config
	configPath := filepath.Join(tmpDir, "honey.yaml")
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"webhook_app": {
				Type:         apps.AppTypeRecipe,
				TargetRecipe: recipePath,
				Target:       "ssh-test",
			},
		},
		Defaults: config.Defaults{
			SSHUser: "testuser",
		},
	}
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	// 6. Setup and start Webserver
	opts := webserver.Options{
		ConfigPath:     configPath,
		Config:         cfg,
		Token:          "test-token",
		SearchRegistry: searchReg,
		ExecRegistry:   execReg,
	}

	baseURL := newTestServer(t, opts)
	// Inject the queue into the server manually since newTestServer doesn't expose it directly in Options,
	// Wait, we can't easily inject the queue into an already built server via newTestServer if it doesn't take Queue as opt.
	// But actually we are running sync: false webhook, so it might not even need the queue. Let's check the recipe.
	// async: false is set. So it doesn't need the queue.

	httpClient := &http.Client{Timeout: 30 * time.Second}

	// 7. Trigger Webhook
	payload := map[string]interface{}{
		"payload": map[string]interface{}{
			"message": map[string]string{
				"id":   "msg-123",
				"text": "hello-from-e2e-webhook",
			},
		},
	}

	resp := doJSON(t, httpClient, baseURL+"/api/v1/webhooks/webhook_app/hook1", payload)

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out webserver.CueExecExecuteResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	resp.Body.Close()
	require.Len(t, out.Results, 1)
	assert.True(t, out.Results[0].Success, "Expected step to succeed, got: %s", out.Results[0].ErrMsg)

	// 8. Verify on SSH container
	client, err := execReg.Dialer.Dial("testuser", sshH, sshP, keyFile)
	require.NoError(t, err)
	defer client.Close()

	output, err := client.Run("cat /tmp/webhook_out.txt")
	require.NoError(t, err)
	// The gjson extract of "payload.message" will stringify the object
	assert.Contains(t, string(output), "msg-123")
	assert.Contains(t, string(output), "hello-from-e2e-webhook")

	// 9. Trigger duplicate webhook
	respDuplicate := doJSON(t, httpClient, baseURL+"/api/v1/webhooks/webhook_app/hook1", payload)
	defer respDuplicate.Body.Close()

	require.Equal(t, http.StatusOK, respDuplicate.StatusCode)

	var dupOut map[string]interface{}
	require.NoError(t, json.NewDecoder(respDuplicate.Body).Decode(&dupOut))
	assert.Equal(t, "duplicate", dupOut["status"])
}
