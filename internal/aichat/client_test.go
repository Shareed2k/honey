package aichat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestComplete_success(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("OPENAI_BASE_URL", "") // client uses default path join
	t.Setenv("OPENAI_MODEL", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Stream {
			http.Error(w, "expected stream false", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(chatResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{{Message: struct {
				Content string `json:"content"`
			}{Content: "hello summary"}}},
		})
	}))
	defer srv.Close()
	_ = os.Setenv("OPENAI_BASE_URL", srv.URL+"/v1")
	defer os.Unsetenv("OPENAI_BASE_URL")

	out, err := Complete(context.Background(), "sys", "user", "gpt-test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello summary" {
		t.Fatalf("got %q", out)
	}
}

func TestComplete_missingKey(t *testing.T) {
	os.Unsetenv("OPENAI_API_KEY")
	_, err := Complete(context.Background(), "s", "u", "m", 0)
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Fatalf("expected missing key error, got %v", err)
	}
}
