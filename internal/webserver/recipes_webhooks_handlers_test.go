package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/queue"
	"github.com/shareed2k/honey/internal/searchrun"
)

type dummyProvider struct{}

func (dummyProvider) ID() string            { return "dummy" }
func (dummyProvider) BackendName() string   { return "dummy" }
func (dummyProvider) CacheIdentity() string { return "dummy" }
func (dummyProvider) Search(_ context.Context, _ hosts.Query) ([]hosts.Record, error) {
	return []hosts.Record{{Provider: "dummy", Name: "localhost", PrimaryIP: "127.0.0.1"}}, nil
}

type dummyFactory struct{}

func (dummyFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	return []hosts.Backend{dummyProvider{}}
}

func (dummyFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return dummyProvider{}
}
func (dummyFactory) BackendRows() []config.BackendRow { return nil }

func TestRecipeWebhook(t *testing.T) {
	tempDir := t.TempDir()
	recipePath := filepath.Join(tempDir, "test.cue")
	cueContent := `
recipe: {
	name: "test-webhook"
	webhooks: {
		"hook1": {
			extract: {
				"HONEY_PROMPT_VAR": "payload.var"
			}
			async: false
		}
	}
	steps: [
		{
			host: "localhost"
			env: {
				HONEY_PROMPT_VAR: string | *""
			}
			command: "echo \(env.HONEY_PROMPT_VAR)"
		}
	]
}
`
	if err := os.WriteFile(recipePath, []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(tempDir, "honey.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"app1": {
				Type:         apps.AppTypeRecipe,
				TargetRecipe: recipePath,
				Target:       "dynamic:localhost",
			},
		},
	}

	opts := Options{
		ConfigPath:     filepath.Join(tempDir, "honey.yaml"),
		Config:         cfg,
		Token:          "dummy",
		SearchRegistry: searchrun.NewRegistry([]searchrun.ProviderFactory{dummyFactory{}}),
	}

	q, _ := queue.NewAntsQueue(5)
	srv, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.webhookQueue = q

	// Create test server
	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	payload := []byte(`{"payload": {"var": "hello-webhook"}}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/app1/hook1", bytes.NewBuffer(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "expected_token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		t.Fatalf("expected 200 OK, got %v, err: %v", resp.StatusCode, errResp["error"])
	}

	var out CueExecExecuteResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	if len(out.Results) == 0 {
		t.Fatalf("expected results")
	}
}

func TestRecipeWebhookAsync(t *testing.T) {
	tempDir := t.TempDir()
	recipePath := filepath.Join(tempDir, "test.cue")
	cueContent := `
recipe: {
	name: "test-webhook"
	webhooks: {
		"hook1": {
			async: true
		}
	}
	steps: [
		{
			host: "localhost"
			command: "echo test"
		}
	]
}
`
	if err := os.WriteFile(recipePath, []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(tempDir, "honey.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"app1": {
				Type:         apps.AppTypeRecipe,
				TargetRecipe: recipePath,
				Target:       "dynamic:localhost",
			},
		},
	}

	opts := Options{
		ConfigPath:     filepath.Join(tempDir, "honey.yaml"),
		Config:         cfg,
		Token:          "dummy",
		RecordDir:      tempDir,
		SearchRegistry: searchrun.NewRegistry([]searchrun.ProviderFactory{dummyFactory{}}),
	}

	q, _ := queue.NewAntsQueue(5)
	srv, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.webhookQueue = q

	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/app1/hook1", bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		var errResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errResp)
		t.Fatalf("expected 202 Accepted, got %v, err: %v", resp.StatusCode, errResp["error"])
	}

	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}

	id := out["id"]
	if id == "" {
		t.Fatalf("expected recording id")
	}

	// Poll results
	time.Sleep(100 * time.Millisecond) // wait for async execution

	reqGet, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/webhooks/results/"+id, nil)
	reqGet.Header.Set("Authorization", "Bearer dummy")
	respGet, err := http.DefaultClient.Do(reqGet)
	if err != nil {
		t.Fatal(err)
	}
	defer respGet.Body.Close()

	if respGet.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %v", respGet.StatusCode)
	}

	var getOut WebhookResultResponse
	if err := json.NewDecoder(respGet.Body).Decode(&getOut); err != nil {
		t.Fatal(err)
	}

	if getOut.ID != id {
		t.Fatalf("expected id %v, got %v", id, getOut.ID)
	}
	if getOut.Status == "" {
		t.Fatalf("expected status")
	}
}

func TestWebhookRateLimit(t *testing.T) {
	t.Parallel()
	// burst=1: first request consumes the single token, second must be 429.
	s := newTestServer(t, Options{WebhookRatePerSecond: 1, WebhookBurst: 1})

	send := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/myapp/myhook",
			bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w.Code
	}

	_ = send() // consume the single token; result doesn't matter (no recipe configured)
	code := send()
	if code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exhausted, got %d", code)
	}
}

// newWebhookDebugServer builds a test server with one sync recipe app exposing
// webhook "hook1" that extracts payload.var into HONEY_PROMPT_VAR.
func newWebhookDebugServer(t *testing.T) *httptest.Server {
	t.Helper()
	tempDir := t.TempDir()
	recipePath := filepath.Join(tempDir, "test.cue")
	cueContent := `
recipe: {
	name: "test-webhook"
	webhooks: {
		"hook1": {
			extract: {
				"HONEY_PROMPT_VAR": "payload.var"
			}
			async: false
		}
	}
	steps: [
		{
			host: "localhost"
			env: {
				HONEY_PROMPT_VAR: string | *""
			}
			command: "echo \(env.HONEY_PROMPT_VAR)"
		}
	]
}
`
	if err := os.WriteFile(recipePath, []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(tempDir, "honey.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"app1": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "dynamic:localhost"},
		},
	}
	opts := Options{
		ConfigPath:     configPath,
		Config:         cfg,
		Token:          "dummy",
		SearchRegistry: searchrun.NewRegistry([]searchrun.ProviderFactory{dummyFactory{}}),
	}
	q, _ := queue.NewAntsQueue(5)
	srv, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.webhookQueue = q
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)
	return ts
}

func getWebhookDeliveries(t *testing.T, ts *httptest.Server) []WebhookDelivery {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/webhooks/app1/hook1/deliveries", nil)
	req.Header.Set("Authorization", "Bearer dummy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deliveries status %d", resp.StatusCode)
	}
	var out struct {
		Deliveries []WebhookDelivery `json:"deliveries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Deliveries
}

func TestWebhookDebugDryRun(t *testing.T) {
	ts := newWebhookDebugServer(t)
	// Outer "payload" is the debug field; its value is the webhook body.
	body := `{"payload": {"payload": {"var": "hello"}}, "execute": false}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/app1/hook1/debug", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer dummy")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out webhookDebugResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Outcome != "dry_run" || out.Executed {
		t.Fatalf("expected dry_run/not-executed, got %+v", out)
	}
	if out.Extracted["HONEY_PROMPT_VAR"] != "hello" {
		t.Fatalf("extracted = %+v", out.Extracted)
	}
	if len(out.Results) != 0 {
		t.Fatalf("dry-run must not execute; got %d results", len(out.Results))
	}
	if d := getWebhookDeliveries(t, ts); len(d) != 1 || d[0].Source != "dry_run" {
		t.Fatalf("deliveries = %+v", d)
	}
}

func TestWebhookDebugExecute(t *testing.T) {
	ts := newWebhookDebugServer(t)
	body := `{"payload": {"payload": {"var": "hi"}}, "execute": true}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/app1/hook1/debug", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer dummy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out webhookDebugResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Outcome != "executed" || !out.Executed {
		t.Fatalf("expected executed, got %+v", out)
	}
	if len(out.Results) == 0 {
		t.Fatalf("expected results")
	}
	if d := getWebhookDeliveries(t, ts); len(d) != 1 || d[0].Source != "test" || len(d[0].Results) == 0 {
		t.Fatalf("deliveries = %+v", d)
	}
}

func TestWebhookLiveDeliveryCaptured(t *testing.T) {
	ts := newWebhookDebugServer(t)
	payload := []byte(`{"payload": {"var": "live"}}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/app1/hook1", bytes.NewBuffer(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("live status %d", resp.StatusCode)
	}
	d := getWebhookDeliveries(t, ts)
	if len(d) != 1 || d[0].Source != "live" || d[0].Outcome != "executed" || len(d[0].Results) == 0 {
		t.Fatalf("deliveries = %+v", d)
	}
	if d[0].Extracted["HONEY_PROMPT_VAR"] != "live" {
		t.Fatalf("extracted = %+v", d[0].Extracted)
	}
}

// newAsyncWebhookServer builds a test server with session recording enabled and
// an async webhook so deliveries can be enriched from the recording.
func newAsyncWebhookServer(t *testing.T) *httptest.Server {
	t.Helper()
	tempDir := t.TempDir()
	recipePath := filepath.Join(tempDir, "test.cue")
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
			host: "localhost"
			command: "echo async-test"
		}
	]
}
`
	if err := os.WriteFile(recipePath, []byte(cueContent), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(tempDir, "honey.yaml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"app1": {Type: apps.AppTypeRecipe, TargetRecipe: recipePath, Target: "dynamic:localhost"},
		},
	}
	opts := Options{
		ConfigPath:     configPath,
		Config:         cfg,
		Token:          "dummy",
		RecordDir:      tempDir, // enables recording → deliveries enrichment
		SearchRegistry: searchrun.NewRegistry([]searchrun.ProviderFactory{dummyFactory{}}),
	}
	q, _ := queue.NewAntsQueue(5)
	srv, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.webhookQueue = q
	ts := httptest.NewServer(srv.router)
	t.Cleanup(ts.Close)
	return ts
}

func TestWebhookDeliveriesEnrichesAsync(t *testing.T) {
	ts := newAsyncWebhookServer(t)

	body := `{"payload": {}, "execute": true}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/app1/hook1/debug", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer dummy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out webhookDebugResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Outcome != "queued" || out.ExecID == "" {
		t.Fatalf("expected queued + exec_id, got %+v", out)
	}

	// Poll /deliveries until the async row enriches from its recording.
	deadline := time.Now().Add(10 * time.Second)
	for {
		d := getWebhookDeliveries(t, ts)
		if len(d) == 1 && d[0].Outcome != "queued" {
			if d[0].Outcome != "executed" && d[0].Outcome != "failed" {
				t.Fatalf("unexpected enriched outcome: %+v", d[0])
			}
			if len(d[0].Results) == 0 {
				t.Fatalf("enriched row has no results: %+v", d[0])
			}
			return // success
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery never enriched: %+v", d)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestWebhookDeliveriesNoEnrichWithoutRecording(t *testing.T) {
	// newWebhookDebugServer has no RecordDir; async run cannot record → no exec_id,
	// so the queued delivery stays queued with no results (enrichment is a no-op).
	ts := newWebhookDebugServer(t)
	// hook1 here is sync; force the async path by posting to a sync hook won't yield
	// queued. Instead assert the enrichment guard: a sync executed delivery is left
	// alone and a row without exec_id is never marked executed by enrichment.
	body := `{"payload": {"payload": {"var": "x"}}, "execute": true}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/webhooks/app1/hook1/debug", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer dummy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	d := getWebhookDeliveries(t, ts)
	if len(d) != 1 || d[0].ExecID != "" {
		t.Fatalf("sync delivery should have no exec_id: %+v", d)
	}
}

// TestResolveWebhookEnv locks in the env-extract-then-validate sequence
// previously duplicated between the live and debug webhook paths
// (architecture review candidate #2).
func TestResolveWebhookEnv(t *testing.T) {
	webhook := cuetry.RecipeWebhook{Extract: map[string]string{"BRANCH": "ref"}}
	recipe := cuetry.Recipe{}

	envMap, err := resolveWebhookEnv([]byte(`{"ref":"main"}`), webhook, recipe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if envMap["BRANCH"] != "main" {
		t.Fatalf("expected BRANCH=main, got %+v", envMap)
	}
}

func TestResolveWebhookEnv_InvalidPayload(t *testing.T) {
	webhook := cuetry.RecipeWebhook{Extract: map[string]string{"BRANCH": "ref"}}
	if _, err := resolveWebhookEnv([]byte("not json"), webhook, cuetry.Recipe{}); err == nil {
		t.Fatal("expected an error for invalid JSON payload")
	}
}

// TestWebhookSearchHostsInput locks in the SearchHostsInput assembly
// previously duplicated between the live and debug webhook paths.
func TestWebhookSearchHostsInput(t *testing.T) {
	api := &RecipesAPI{opts: Options{ConfigPath: "/etc/honey.yaml", Config: &config.File{}}}

	app := apps.AppConfig{Target: "web-*", Provider: "aws", Backend: "prod"}
	in := api.webhookSearchHostsInput(app)
	if in.Name != "web-*" || in.Providers != "aws" || in.Backends != "prod" || in.NameRegex != "" {
		t.Fatalf("unexpected search input: %+v", in)
	}

	regexApp := apps.AppConfig{TargetRegex: "^web-\\d+$"}
	in = api.webhookSearchHostsInput(regexApp)
	if in.NameRegex != "^web-\\d+$" {
		t.Fatalf("expected NameRegex to be set when Target is empty, got %+v", in)
	}
}

// TestDeriveWebhookIdempotencyKey locks in the shared key-derivation logic;
// the header case is the one genuine difference between the live path (real
// request headers available) and the debug path (headerLookup is nil).
func TestDeriveWebhookIdempotencyKey(t *testing.T) {
	body := []byte(`{"id":"abc123"}`)

	t.Run("json path", func(t *testing.T) {
		webhook := cuetry.RecipeWebhook{IdempotencyKey: "id"}
		got := deriveWebhookIdempotencyKey(webhook, body, nil)
		if got != "abc123" {
			t.Fatalf("got %q, want abc123", got)
		}
	})

	t.Run("header with lookup", func(t *testing.T) {
		webhook := cuetry.RecipeWebhook{IdempotencyKey: "header:X-Delivery-Id"}
		got := deriveWebhookIdempotencyKey(webhook, body, func(name string) string {
			if name == "X-Delivery-Id" {
				return "hdr-1"
			}
			return ""
		})
		if got != "hdr-1" {
			t.Fatalf("got %q, want hdr-1", got)
		}
	})

	t.Run("header without lookup yields empty", func(t *testing.T) {
		webhook := cuetry.RecipeWebhook{IdempotencyKey: "header:X-Delivery-Id"}
		got := deriveWebhookIdempotencyKey(webhook, body, nil)
		if got != "" {
			t.Fatalf("got %q, want empty (no request headers available)", got)
		}
	})

	t.Run("no idempotency_key falls back to body hash", func(t *testing.T) {
		webhook := cuetry.RecipeWebhook{}
		got := deriveWebhookIdempotencyKey(webhook, body, nil)
		if len(got) != 64 { // hex-encoded sha256
			t.Fatalf("expected a 64-char hex hash, got %q", got)
		}
	})
}
