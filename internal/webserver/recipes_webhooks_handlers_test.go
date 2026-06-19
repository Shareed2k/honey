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
	ts := httptest.NewServer(srv.mux)
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

	ts := httptest.NewServer(srv.mux)
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
