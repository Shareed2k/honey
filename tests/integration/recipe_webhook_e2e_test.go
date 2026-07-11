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
			command: "sleep 2 && echo $WEBHOOK_MSG > /tmp/webhook_out.txt"
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

	// 7. Trigger Webhook in background so we can trigger a duplicate
	payload := map[string]interface{}{
		"payload": map[string]interface{}{
			"message": map[string]string{
				"id":   "msg-123",
				"text": "hello-from-e2e-webhook",
			},
		},
	}

	go func() {
		resp := doJSON(t, httpClient, baseURL+"/api/v1/webhooks/webhook_app/hook1", payload)
		defer resp.Body.Close()
	}()

	time.Sleep(500 * time.Millisecond) // Give the first request time to acquire the dedup lock

	// 8. Trigger duplicate webhook while the first one is sleeping
	respDuplicate := doJSON(t, httpClient, baseURL+"/api/v1/webhooks/webhook_app/hook1", payload)
	defer respDuplicate.Body.Close()

	require.Equal(t, http.StatusOK, respDuplicate.StatusCode)

	var dupOut map[string]interface{}
	require.NoError(t, json.NewDecoder(respDuplicate.Body).Decode(&dupOut))
	t.Logf("Duplicate Response: %v", dupOut)
	assert.Equal(t, "duplicate", dupOut["status"])

	// 9. Verify on SSH container
	// Wait enough for the first command (which sleeps for 2s) to finish
	time.Sleep(3 * time.Second)

	client, err := execReg.Dialer.Dial("testuser", sshH, sshP, keyFile)
	require.NoError(t, err)
	defer client.Close()

	output, err := client.Run("cat /tmp/webhook_out.txt")
	require.NoError(t, err)
	// The gjson extract of "payload.message" will stringify the object
	assert.Contains(t, string(output), "msg-123")
	assert.Contains(t, string(output), "hello-from-e2e-webhook")
}

// TestWebhookDeliveriesEnrichmentE2E fires a live async webhook against a real
// SSH container, then verifies the deliveries endpoint enriches the captured
// (queued) row with the final outcome + host results once the recording lands.
func TestWebhookDeliveriesEnrichmentE2E(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)
	searchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{webhookTestFactory{rec: rec}})
	execReg := &testRegistry{Dialer: newTestDialer(sshH, sshP, keyFile)}

	tmpDir := t.TempDir()
	recipePath := filepath.Join(tmpDir, "webhook_async.cue")
	cueContent := `
recipe: {
	name: "test-webhook-async"
	webhooks: {
		"hook1": {
			async: true
		}
	}
	steps: [
		{
			host: "*"
			command: "echo enrich-e2e"
		}
	]
}
`
	require.NoError(t, os.WriteFile(recipePath, []byte(cueContent), 0o600))

	configPath := filepath.Join(tmpDir, "honey.yaml")
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"webhook_app": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "ssh-test"},
		},
		Defaults: config.Defaults{SSHUser: "testuser"},
	}
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	opts := webserver.Options{
		ConfigPath:     configPath,
		Config:         cfg,
		Token:          "test-token",
		RecordDir:      tmpDir, // enables recording → enrichment
		SearchRegistry: searchReg,
		ExecRegistry:   execReg,
	}
	baseURL := newTestServer(t, opts)
	httpClient := &http.Client{Timeout: 30 * time.Second}

	// Fire the live async webhook → 202 queued.
	resp := doJSON(t, httpClient, baseURL+"/api/v1/webhooks/webhook_app/hook1", map[string]interface{}{})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	resp.Body.Close()

	// Poll the authed deliveries endpoint until the queued row enriches.
	deadline := time.Now().Add(20 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/webhooks/webhook_app/hook1/deliveries", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		dResp, err := httpClient.Do(req)
		require.NoError(t, err)
		var out struct {
			Deliveries []webserver.WebhookDelivery `json:"deliveries"`
		}
		require.NoError(t, json.NewDecoder(dResp.Body).Decode(&out))
		dResp.Body.Close()

		if len(out.Deliveries) == 1 && out.Deliveries[0].Outcome != "queued" {
			d := out.Deliveries[0]
			assert.Contains(t, []string{"executed", "failed"}, d.Outcome)
			assert.NotEmpty(t, d.Results, "enriched delivery should carry host results")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery never enriched: %+v", out.Deliveries)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
