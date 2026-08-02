package alertwebhook

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	amtemplate "github.com/prometheus/alertmanager/template"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 9095, cfg.Port)
	assert.Equal(t, "1h", cfg.DedupWindow)
	assert.Equal(t, 10000, cfg.DedupCapacity)
}

func TestNewServer(t *testing.T) {
	cfg := config.AlertWebhookConfig{
		Port:          8080,
		DedupWindow:   "2h",
		DedupCapacity: 500,
	}
	srv, err := New(cfg, nil, "", nil)
	require.NoError(t, err)
	assert.Equal(t, 2*time.Hour, srv.dedupWindow)
	assert.NotNil(t, srv.seen)
}

func TestNewServer_BadWindow(t *testing.T) {
	cfg := config.AlertWebhookConfig{
		DedupWindow: "invalid",
	}
	_, err := New(cfg, nil, "", nil)
	assert.ErrorContains(t, err, "dedup_window:")
}

func TestHandleAlert_Methods(t *testing.T) {
	srv, err := New(DefaultConfig(), nil, "", searchrun.NewRegistry(nil))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/webhook/alert", nil)
	rec := httptest.NewRecorder()

	srv.handleAlert(rec, req)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestHandleAlert_Unauthorized(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "secret-token"
	srv, err := New(cfg, nil, "", nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()

	srv.handleAlert(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestHandleAlert_Authorized(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "secret-token"
	srv, err := New(cfg, nil, "", nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	srv.handleAlert(rec, req)
	// It parses JSON but `{}` doesn't fail json.Unmarshal for webhook.Message
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestHandleAlert_InvalidJSON(t *testing.T) {
	srv, err := New(DefaultConfig(), nil, "", searchrun.NewRegistry(nil))
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/webhook/alert", bytes.NewReader([]byte("invalid json")))
	rec := httptest.NewRecorder()

	srv.handleAlert(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid JSON")
}

func TestIsDuplicate(t *testing.T) {
	srv, err := New(DefaultConfig(), nil, "", searchrun.NewRegistry(nil))
	require.NoError(t, err)

	assert.False(t, srv.isDuplicate(""))

	fp := "fp1"
	assert.False(t, srv.isDuplicate(fp)) // First time is false
	assert.True(t, srv.isDuplicate(fp))  // Second time is true
}

func TestEvalHostQuery(t *testing.T) {
	query := "prod-{{ .region }}-{{ .app }}"
	labels := amtemplate.KV{
		"region": "eu",
		"app":    "web",
	}

	res, err := evalHostQuery(query, labels)
	require.NoError(t, err)
	assert.Equal(t, "prod-eu-web", res)
}

func TestEvalHostQuery_InvalidTemplate(t *testing.T) {
	query := "prod-{{ .region "
	_, err := evalHostQuery(query, nil)
	assert.Error(t, err)
}

func TestResolveMapping(t *testing.T) {
	fileCfg := &config.File{
		AlertMappings: []config.AlertMapping{
			{
				MatchLabels: map[string]string{
					"env": "prod",
					"app": "web",
				},
				HostQuery: "prod-web",
			},
		},
	}

	// Match
	labels := amtemplate.KV{"env": "prod", "app": "web", "other": "value"}
	mapping := resolveMapping(fileCfg, labels)
	assert.NotNil(t, mapping)
	assert.Equal(t, "prod-web", mapping.HostQuery)

	// No match
	labels2 := amtemplate.KV{"env": "prod", "app": "api"}
	mapping2 := resolveMapping(fileCfg, labels2)
	assert.Nil(t, mapping2)

	// Nil config
	assert.Nil(t, resolveMapping(nil, labels))
}

func TestSendHTTP(t *testing.T) {
	// Start a test server
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "custom-val", r.Header.Get("X-Custom"))
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &config.AlertNotifyHTTP{
		URL: ts.URL,
		Headers: map[string]string{
			"X-Custom": "custom-val",
		},
	}

	sendHTTP(context.Background(), cfg, "test subject", "test body")
	assert.True(t, called)
}

func TestSendSlack(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true

		var payload map[string]string
		err := json.NewDecoder(r.Body).Decode(&payload)
		require.NoError(t, err)

		assert.Equal(t, "C123", payload["channel"])
		assert.True(t, strings.Contains(payload["text"], "test subject"))

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := &config.AlertNotifySlack{
		WebhookURL: ts.URL,
		ChannelID:  "C123",
	}

	sendSlack(context.Background(), cfg, "test subject", "test body")
	assert.True(t, called)
}

func TestSendTelegram(t *testing.T) {
	called := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		assert.Equal(t, "/bottoken123/sendMessage", r.URL.Path)
		var payload map[string]string
		err := json.NewDecoder(r.Body).Decode(&payload)
		require.NoError(t, err)
		assert.Contains(t, []string{"111", "222"}, payload["chat_id"])
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// The Telegram endpoint is hardcoded to api.telegram.org in the code,
	// so to test it we would need to mock http.DefaultClient or refactor the code to allow base URL overrides.
	// Since we can't easily override the URL without changing the code, we will trigger the function
	// with an empty bot token to ensure it bails out early, and try a failing request to cover the error path.

	// Test empty bot token (returns early)
	cfg := &config.AlertNotifyTelegram{BotToken: ""}
	sendTelegram(context.Background(), cfg, "subject", "body")

	// Test with a dummy token that will hit the real api.telegram.org but fail gracefully
	// We'll pass a short context to make it fail fast
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	cfg2 := &config.AlertNotifyTelegram{BotToken: "dummy", ChatIDs: []string{"123"}}
	sendTelegram(ctx, cfg2, "subject", "body")
}

func TestSendNotifications(_ *testing.T) {
	// Simple test to ensure routing to sub-functions doesn't panic
	cfg := &config.AlertNotify{
		HTTP:     &config.AlertNotifyHTTP{URL: "http://localhost:12345/fail-fast"},
		Slack:    &config.AlertNotifySlack{WebhookURL: "http://localhost:12345/fail-fast"},
		Telegram: &config.AlertNotifyTelegram{BotToken: "dummy", ChatIDs: []string{"123"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	labels := amtemplate.KV{"alertname": "TestAlert"}
	sendNotifications(ctx, cfg, "body", labels)
}

func TestListenAndServe(t *testing.T) {
	// Use an ephemeral port
	cfg := DefaultConfig()
	cfg.Port = 0 // Should default to 9095, but we don't want to conflict, so let's use a dynamic one if possible.
	// Wait, the code defaults to 9095 if <= 0. Let's use 0 and let it fail to bind if it conflicts, or just cancel fast.
	// Actually, we can use a random high port to avoid conflicts.
	cfg.Port = 19095

	srv, err := New(cfg, nil, "", nil)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error)
	go func() {
		errCh <- srv.ListenAndServe(ctx)
	}()

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to shut it down
	cancel()

	err = <-errCh
	assert.NoError(t, err) // Expect nil on successful shutdown
}

func TestInvestigate(t *testing.T) {
	// We just want to ensure it bails out correctly without panicking.
	// Testing full engine.ExecuteSSHParallel requires a lot of mocking.
	// We'll test the early returns.

	srv, err := New(DefaultConfig(), nil, "", searchrun.NewRegistry(nil))
	require.NoError(t, err)

	ctx := context.Background()

	// 1. No mapping
	srv.investigate(ctx, amtemplate.Alert{Labels: amtemplate.KV{"app": "unknown"}})

	// 2. Mapping exists, host_query fails
	srv.fileCfg = &config.File{
		AlertMappings: []config.AlertMapping{
			{
				MatchLabels: map[string]string{"app": "test"},
				HostQuery:   "{{ .invalid template syntax }}",
			},
		},
	}
	srv.investigate(ctx, amtemplate.Alert{Labels: amtemplate.KV{"app": "test"}})

	// 3. Mapping exists, host_query succeeds, but no hosts found (hostapi.SearchHosts returns 0)
	srv.fileCfg.AlertMappings[0].HostQuery = "nonexistent-host"
	srv.investigate(ctx, amtemplate.Alert{Labels: amtemplate.KV{"app": "test"}})
}
