package webserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddlewareRejectsMissingToken(t *testing.T) {
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "test-token-xyz",
		Version:    "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/meta", nil)
	s.withAuth(s.handleMeta)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddlewareAcceptsBearer(t *testing.T) {
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Version:    "1.2.3",
		Commit:     "c",
		Date:       "d",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/meta", nil)
	req.Header.Set("Authorization", "Bearer secret")
	s.withAuth(s.handleMeta)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
