package webserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveToken_EnvPrecedence(t *testing.T) {
	t.Setenv(webTokenEnv, "fixed-token")
	got, err := ResolveToken(t.TempDir())
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if got != "fixed-token" {
		t.Fatalf("want env token %q, got %q", "fixed-token", got)
	}
}

func TestResolveToken_GeneratesAndPersists(t *testing.T) {
	t.Setenv(webTokenEnv, "") // ensure env path is not taken
	dir := t.TempDir()

	first, err := ResolveToken(dir)
	if err != nil {
		t.Fatalf("ResolveToken (first): %v", err)
	}
	if first == "" {
		t.Fatal("expected a generated token, got empty")
	}
	if _, err := os.Stat(filepath.Join(dir, webTokenFile)); err != nil {
		t.Fatalf("token file not persisted: %v", err)
	}

	second, err := ResolveToken(dir)
	if err != nil {
		t.Fatalf("ResolveToken (second): %v", err)
	}
	if second != first {
		t.Fatalf("token not stable across calls: first=%q second=%q", first, second)
	}
}

func TestResolveToken_EmptyStateDirIsEphemeral(t *testing.T) {
	t.Setenv(webTokenEnv, "")
	got, err := ResolveToken("")
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if got == "" {
		t.Fatal("expected a generated token with empty state dir")
	}
}

func TestAuthorized_DisableAuth(t *testing.T) {
	withAuthOff := &Server{opts: Options{DisableAuth: true}}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
	if !withAuthOff.authorized(req) {
		t.Fatal("DisableAuth=true should authorize any request")
	}

	withAuthOn := &Server{opts: Options{Token: "tok"}}
	if withAuthOn.authorized(req) {
		t.Fatal("missing token should not be authorized")
	}
	req.Header.Set("X-Honey-Token", "tok")
	if !withAuthOn.authorized(req) {
		t.Fatal("matching X-Honey-Token should be authorized")
	}
}

func TestWithAuth_Unauthorized(t *testing.T) {
	s := &Server{opts: Options{Token: "tok"}}
	called := false
	h := s.withAuth(func(http.ResponseWriter, *http.Request) { called = true })

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("handler should not run without a valid token")
	}
}

// TestIndexCookie_SetThenCookieAuthorizes verifies the docker-fix path: a tokened
// page load sets the honey_proxy_token cookie (without stripping the token from the
// URL, so the SPA can still read it), and a subsequent request carrying only that
// cookie (no query, no header) is authorized.
func TestIndexCookie_SetThenCookieAuthorizes(t *testing.T) {
	s := &Server{opts: Options{Token: "tok"}}

	// 1. GET /?token=tok through the static wrapper -> Set-Cookie + passthrough (no redirect).
	served := false
	wrapped := s.withIndexCookie(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?token=tok", nil))
	if rec.Code != http.StatusOK || !served {
		t.Fatalf("want 200 passthrough serving the UI, got code=%d served=%v", rec.Code, served)
	}
	cookies := rec.Result().Cookies()
	var tokenCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "honey_proxy_token" {
			tokenCookie = c
		}
	}
	if tokenCookie == nil || tokenCookie.Value != "tok" {
		t.Fatalf("expected honey_proxy_token cookie set to %q, got %+v", "tok", cookies)
	}

	// 2. A protected request carrying only the cookie must be authorized.
	called := false
	api := s.withAuth(func(http.ResponseWriter, *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
	req.AddCookie(tokenCookie)
	rec2 := httptest.NewRecorder()
	api(rec2, req)
	if rec2.Code == http.StatusUnauthorized || !called {
		t.Fatalf("cookie-only request should be authorized, got code=%d called=%v", rec2.Code, called)
	}
}
