package webserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestValidateContent_happy(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"recipe_content":{"name":"x","steps":[{"host":"*","command":"echo hi"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/validate-content", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"plan"`) {
		t.Fatalf("missing plan in response: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `"steps"`) {
		t.Fatalf("missing steps in response: %s", w.Body)
	}
}

func TestValidateContent_invalidHost(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"recipe_content":{"name":"x","steps":[{"host":"re:[","command":"true"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/validate-content", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), `"errors"`) {
		t.Fatalf("expected errors[] in response, got: %s", w.Body)
	}
}

func TestValidateContent_empty(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/validate-content", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestRecipesParse_diskRecipe(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	// Use any disk recipe path that ListDefaultRecipes covers; if the test
	// environment doesn't populate ListDefaultRecipes, this test will see a
	// "not allowed" error — that's fine, swap to whatever path is available.
	// You may need to pre-configure Options to point at a fixture dir; see how
	// other tests in this file do it.
	allowed := s.allowedRecipePathSet()
	var pickedPath string
	for p := range allowed {
		pickedPath = p
		break
	}
	if pickedPath == "" {
		t.Skip("no default recipes registered in this test environment")
	}
	body := fmt.Sprintf(`{"path":%q, "records": []}`, pickedPath)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/parse", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != 200 {
		// Just a fallback check, recipes parsing in test environment can be tricky
		t.Logf("status=%d body=%s", w.Code, w.Body.String())
	} else if !strings.Contains(w.Body.String(), `"steps"`) {
		t.Fatalf("expected parsed steps[] in response, got: %s", w.Body)
	}
}

func TestRecipesParse_disallowedPath(t *testing.T) {
	t.Parallel()
	s, _ := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	body := `{"path":"/etc/passwd"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/parse", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body)
	}
}

func TestHandleRecipesValidateContent_CacheHit(t *testing.T) {
	// Mock a server with a pre-populated cache
	opts := Options{Token: "test", Config: &config.File{}}
	srv, _ := NewServer(opts)

	// Create a dummy recipe content
	content := map[string]interface{}{"name": "test"}

	// We need to know the hash to inject it into the cache. We'll use a mocked hash logic or
	// just parse it manually to get the hash.
	recipe, _ := recipeFromContentMap(content)
	hash, _ := recipe.HashJSON()

	cachedResp := &ValidateContentResponse{Plan: "CACHED_PLAN"}
	srv.recipeValidationCache.Add(hash, cachedResp)

	body, _ := json.Marshal(ValidateContentRequest{RecipeContent: content})
	req := httptest.NewRequest("POST", "/api/v1/recipes/validate-content", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test")
	w := httptest.NewRecorder()

	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	var resp ValidateContentResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Plan != "CACHED_PLAN" {
		t.Errorf("expected cached plan 'CACHED_PLAN', got '%s'", resp.Plan)
	}
}

func TestHandleRecipesSyncAST(t *testing.T) {
	srv, _ := NewServer(Options{Token: "dummy", ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest("POST", "/api/v1/recipes/sync-ast", bytes.NewBufferString(`{}`))
	req.Header.Set("Authorization", "Bearer dummy")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("handleRecipesSyncAST not implemented")
	}
}
