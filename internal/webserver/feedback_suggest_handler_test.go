package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestHandleLogsFeedbackSuggest(t *testing.T) {
	// Start a mock OpenAI LLM server
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Mock OpenAI Chat Completion response format
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
						"content": `{"anomaly":true,"score":0.95,"reason":"unauthorized access"}`,
					},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer llmServer.Close()

	// Initialize the Server with config pointing to the mock LLM server
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

	t.Run("success suggestion", func(t *testing.T) {
		reqBody := `{"line":"error: authentication failed for root","source":"auth.log"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/feedback/suggest", strings.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.handleLogsFeedbackSuggest(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d. body: %s", rec.Code, rec.Body.String())
		}

		var resp feedbackSuggestResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}

		if !resp.Anomaly {
			t.Error("expected anomaly to be true")
		}
		if resp.Score != 0.95 {
			t.Errorf("expected score 0.95, got %f", resp.Score)
		}
		if resp.Reason != "unauthorized access" {
			t.Errorf("expected reason 'unauthorized access', got %s", resp.Reason)
		}
	})

	t.Run("empty line validation", func(t *testing.T) {
		reqBody := `{"line":"","source":"auth.log"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/feedback/suggest", strings.NewReader(reqBody))
		rec := httptest.NewRecorder()

		s.handleLogsFeedbackSuggest(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400 for empty line, got %d", rec.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/logs/feedback/suggest", nil)
		rec := httptest.NewRecorder()

		s.handleLogsFeedbackSuggest(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", rec.Code)
		}
	})

	t.Run("malformed json request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/logs/feedback/suggest", bytes.NewReader([]byte("{invalid-json")))
		rec := httptest.NewRecorder()

		s.handleLogsFeedbackSuggest(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", rec.Code)
		}
	})
}
