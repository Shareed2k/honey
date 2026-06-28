package webserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/policy"
)

// denyStoreMutationsRego allows api_request (auth middleware) but denies recipe_save/recipe_delete.
// This ensures the OPA gate tests exercise the store-specific gate, not the auth middleware gate.
const denyStoreMutationsRego = `package honey
import rego.v1
default allow := true
allow := false if { input.action == "recipe_save" }
allow := false if { input.action == "recipe_delete" }
`

// denyStoreReadsRego allows api_request but denies recipe_read/recipe_list.
const denyStoreReadsRego = `package honey
import rego.v1
default allow := true
allow := false if { input.action == "recipe_read" }
allow := false if { input.action == "recipe_list" }
`

// allowAllRego allows every action, including api_request, recipe_save, recipe_delete.
const allowAllRego = "package honey\nimport rego.v1\ndefault allow := true\n"

func mustEnforcer(t *testing.T, src string) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "gate.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	return enf
}

func storeTestServer(t *testing.T, dir string, enf *policy.Enforcer) *Server {
	t.Helper()
	cfg := &config.File{}
	cfg.Defaults.Studio.RecipesPath = dir
	return newTestServer(t, Options{Enforcer: enf, Config: cfg})
}

func TestStoreSave_OPA_deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := storeTestServer(t, dir, mustEnforcer(t, denyStoreMutationsRego))

	body := `{"content": "package honey\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/store/test.cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreSave_OPA_allow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := storeTestServer(t, dir, mustEnforcer(t, allowAllRego))

	body := `{"content": "package honey\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/store/test.cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreSave_nilEnforcer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := storeTestServer(t, dir, nil)

	body := `{"content": "package honey\n"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/store/test.cue", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreDelete_OPA_deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.cue"), []byte("package honey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := storeTestServer(t, dir, mustEnforcer(t, denyStoreMutationsRego))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/recipes/store/test.cue", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreDelete_OPA_allow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.cue"), []byte("package honey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := storeTestServer(t, dir, mustEnforcer(t, allowAllRego))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/recipes/store/test.cue", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreGet_OPA_deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := storeTestServer(t, dir, mustEnforcer(t, denyStoreReadsRego))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/store/test.cue", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreGet_OPA_allow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.cue"), []byte(`
package honey
recipe: name: "test"
steps: []
`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := storeTestServer(t, dir, mustEnforcer(t, allowAllRego))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/store/test.cue", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreList_OPA_deny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := storeTestServer(t, dir, mustEnforcer(t, denyStoreReadsRego))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/store/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body)
	}
}

func TestStoreList_OPA_allow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := storeTestServer(t, dir, mustEnforcer(t, allowAllRego))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes/store/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
}
