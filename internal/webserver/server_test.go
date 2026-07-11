package webserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shareed2k/honey/internal/config"
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
	s.router.ServeHTTP(rec, req)
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
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestServerInitializesCaches(t *testing.T) {
	opts := Options{Token: "test", Config: &config.File{}}
	srv, err := NewServer(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// recipeValidationCache/recipeGraphCache are only ever read through
	// RecipesAPI (nothing on Server itself uses them), so they're owned and
	// constructed there — not duplicated onto the Server struct too
	// (architecture review candidate #6).
	if srv.recipesAPI == nil {
		t.Fatal("expected recipesAPI to be initialized")
	}
	if srv.recipesAPI.recipeValidationCache == nil {
		t.Errorf("expected recipesAPI.recipeValidationCache to be initialized")
	}
	if srv.recipesAPI.recipeGraphCache == nil {
		t.Errorf("expected recipesAPI.recipeGraphCache to be initialized")
	}
}
