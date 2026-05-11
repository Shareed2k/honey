package recipenotify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildFromEnv_HTTPDefaultJSON(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv(EnvHTTPURL, srv.URL)
	t.Setenv(EnvSlackWebhookURL, "")
	t.Setenv(EnvTelegramBotToken, "")
	t.Setenv(EnvTelegramChatIDs, "")

	n, ok := BuildFromEnvFilter(nil)
	if !ok || n == nil {
		t.Fatal("expected notifier")
	}
	if err := n.Send(t.Context(), "subj", "body"); err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(gotBody), &m); err != nil {
		t.Fatalf("json: %v body=%q", err, gotBody)
	}
	if m["subject"] != "subj" || m["message"] != "body" {
		t.Fatalf("unexpected payload: %#v", m)
	}
}

func TestSlackWebhookPayload_noChannel(t *testing.T) {
	p := slackWebhookPayload("S", "M", "")
	m, ok := p.(map[string]string)
	if !ok {
		t.Fatalf("got %T", p)
	}
	if _, has := m["channel"]; has {
		t.Fatalf("unexpected channel: %#v", m)
	}
	if !strings.Contains(m["text"], "S") || !strings.Contains(m["text"], "M") {
		t.Fatalf("got %#v", m)
	}
}

func TestSlackWebhookPayload_withChannel(t *testing.T) {
	p := slackWebhookPayload("S", "M", "C01234567")
	m, ok := p.(map[string]string)
	if !ok {
		t.Fatalf("got %T", p)
	}
	if m["channel"] != "C01234567" {
		t.Fatalf("got %#v", m)
	}
}

func TestParseTelegramChatIDs_skipsInvalid(t *testing.T) {
	t.Setenv(EnvTelegramBotToken, "")
	t.Setenv(EnvTelegramChatIDs, "not-a-number,123")
	// No token → no service
	if tg := buildTelegram(); tg != nil {
		t.Fatal("expected nil without token")
	}
}

func TestEnvHasAnyReceiver(t *testing.T) {
	t.Setenv(EnvHTTPURL, "")
	t.Setenv(EnvSlackWebhookURL, "")
	t.Setenv(EnvTelegramBotToken, "")
	t.Setenv(EnvTelegramChatIDs, "")
	if EnvHasAnyReceiver() {
		t.Fatal("expected false when all empty")
	}
	t.Setenv(EnvHTTPURL, "http://example.com")
	if !EnvHasAnyReceiver() {
		t.Fatal("expected true for HTTP")
	}
}

func TestBuildFromEnv_SlackWebhookJSON(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv(EnvHTTPURL, "")
	t.Setenv(EnvSlackWebhookURL, srv.URL)
	t.Setenv(EnvTelegramBotToken, "")
	t.Setenv(EnvTelegramChatIDs, "")

	n, ok := BuildFromEnvFilter(nil)
	if !ok || n == nil {
		t.Fatal("expected notifier")
	}
	if err := n.Send(t.Context(), "Title", "Hello"); err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(gotBody), &m); err != nil {
		t.Fatalf("json: %v body=%q", err, gotBody)
	}
	if !strings.Contains(m["text"], "Title") || !strings.Contains(m["text"], "Hello") {
		t.Fatalf("unexpected slack payload: %#v", m)
	}
}

func TestBuildFromEnvFilter_slackOnly(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv(EnvHTTPURL, "http://ignored.example/hook")
	t.Setenv(EnvSlackWebhookURL, srv.URL)
	t.Setenv(EnvTelegramBotToken, "token")
	t.Setenv(EnvTelegramChatIDs, "1")

	f := &ServiceFilter{
		Restrict:      true,
		AllowHTTP:     false,
		AllowSlack:    true,
		AllowTelegram: false,
	}
	n, ok := BuildFromEnvFilter(f)
	if !ok || n == nil {
		t.Fatal("expected notifier")
	}
	if err := n.Send(t.Context(), "T", "B"); err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(gotBody), &m); err != nil {
		t.Fatalf("json: %v body=%q", err, gotBody)
	}
	if !strings.Contains(m["text"], "T") {
		t.Fatalf("expected slack text, got %#v", m)
	}
}

func TestBuildFromEnvFilter_slackChannelInJSON(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv(EnvHTTPURL, "")
	t.Setenv(EnvSlackWebhookURL, srv.URL)
	t.Setenv(EnvTelegramBotToken, "")
	t.Setenv(EnvTelegramChatIDs, "")

	f := &ServiceFilter{Restrict: true, AllowSlack: true, SlackChannelID: "C999"}
	n, ok := BuildFromEnvFilter(f)
	if !ok || n == nil {
		t.Fatal("expected notifier")
	}
	if err := n.Send(t.Context(), "S", "M"); err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(gotBody), &m); err != nil {
		t.Fatalf("json: %v body=%q", err, gotBody)
	}
	if m["channel"] != "C999" {
		t.Fatalf("want channel C999, got %#v", m)
	}
}

func TestEnvHasReceiverMatchingFilter_restrict(t *testing.T) {
	t.Setenv(EnvHTTPURL, "http://x")
	t.Setenv(EnvSlackWebhookURL, "")
	t.Setenv(EnvTelegramBotToken, "")
	t.Setenv(EnvTelegramChatIDs, "")

	if !EnvHasReceiverMatchingFilter(&ServiceFilter{Restrict: true, AllowHTTP: true}) {
		t.Fatal("expected true for http selected + env")
	}
	if EnvHasReceiverMatchingFilter(&ServiceFilter{Restrict: true, AllowSlack: true}) {
		t.Fatal("expected false slack selected but no env")
	}
	if !EnvHasReceiverMatchingFilter(nil) {
		t.Fatal("nil filter should match EnvHasAnyReceiver when HTTP env is set")
	}
}
