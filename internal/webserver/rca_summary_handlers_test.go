package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestHandleLogsRCA(t *testing.T) {
	// Start a mock OpenAI LLM server
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion",
			"created": 123456789,
			"model":   "llama3",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "### Root Cause Analysis\nThis is a test root cause.\n\n### Impact Assessment\nModerate impact.\n\n### Actionable Remediation\nRestart the system.",
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer llmServer.Close()

	cfg := &config.File{
		Defaults: config.Defaults{
			Logs: config.Logs{
				AnomalyEndpoint: llmServer.URL,
				AnomalyLLMModel: "llama3",
				AnomalyThresh:   0.90,
			},
		},
	}

	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Config:     cfg,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success RCA", func(t *testing.T) {
		reqBody := `{"anomaly_line":"out of memory error","context":["starting worker 1","worker 1 active"],"source":"syslog"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/rca", strings.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.handleLogsRCA(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d. body: %s", rec.Code, rec.Body.String())
		}

		var resp rcaResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(resp.Markdown, "Root Cause Analysis") {
			t.Errorf("expected response to contain 'Root Cause Analysis', got: %s", resp.Markdown)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/rca", nil)
		rec := httptest.NewRecorder()

		s.handleLogsRCA(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", rec.Code)
		}
	})

	t.Run("malformed json request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/rca", bytes.NewReader([]byte("{invalid-json")))
		rec := httptest.NewRecorder()

		s.handleLogsRCA(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}
	})
}

func TestHandleLogsSummary(t *testing.T) {
	// Start a mock OpenAI LLM server
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"id":      "chatcmpl-mock",
			"object":  "chat.completion",
			"created": 123456789,
			"model":   "llama3",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":    "assistant",
						"content": "Overall system health is normal with minor OOM alerts in worker pools.",
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer llmServer.Close()

	cfg := &config.File{
		Defaults: config.Defaults{
			Logs: config.Logs{
				AnomalyEndpoint: llmServer.URL,
				AnomalyLLMModel: "llama3",
				AnomalyThresh:   0.90,
			},
		},
	}

	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Config:     cfg,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("success summary", func(t *testing.T) {
		reqBody := `{"stats":[{"template":"connection refused","count":42,"score":0.95},{"template":"normal message","count":1000,"score":0.1}]}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/summary", strings.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.handleLogsSummary(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d. body: %s", rec.Code, rec.Body.String())
		}

		var resp summaryResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(resp.Markdown, "Overall system health") {
			t.Errorf("expected response to contain 'Overall system health', got: %s", resp.Markdown)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/summary", nil)
		rec := httptest.NewRecorder()

		s.handleLogsSummary(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", rec.Code)
		}
	})

	t.Run("malformed json request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/summary", bytes.NewReader([]byte("{invalid-json")))
		rec := httptest.NewRecorder()

		s.handleLogsSummary(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}
	})
}

func TestCallDirectLLM_ErrorCases(t *testing.T) {
	// Temporarily clear environment variables to ensure we can test error conditions
	origKey := os.Getenv("OPENAI_API_KEY")
	origBase := os.Getenv("OPENAI_BASE_URL")
	os.Setenv("OPENAI_API_KEY", "")
	os.Setenv("OPENAI_BASE_URL", "")
	defer func() {
		os.Setenv("OPENAI_API_KEY", origKey)
		os.Setenv("OPENAI_BASE_URL", origBase)
	}()

	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Config:     nil, // no config default endpoint
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("no LLM provider configured", func(t *testing.T) {
		// When endpoint is empty and OPENAI_API_KEY is empty, it must return an error
		_, err := s.callDirectLLM(t.Context(), "", "", "hello")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "no LLM provider is configured") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}
