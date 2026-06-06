package webserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecretsEncrypt_MissingKey(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}

	body := `{"plaintext":"mysecret"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/secrets/encrypt", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	s.withAuth(s.handleSecretsEncrypt)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "stack data key: set defaults.secretsprovider") {
		t.Fatalf("expected provider error, got %s", w.Body)
	}
}
