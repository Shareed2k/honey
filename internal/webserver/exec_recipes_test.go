package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRecipesViewUnauthorized(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "tok",
		Version:    "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"path":"/tmp/x.cue"}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/view", body)
	s.withAuth(s.handleRecipesView)(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRecipesViewRejectsDisallowedPath(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "tok",
		Version:    "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]string{"path": "/etc/passwd"})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/view", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer tok")
	s.withAuth(s.handleRecipesView)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestExecRejectsEmptyRecords(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "tok",
		Version:    "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(execRequest{
		SSHUser: "u",
		Command: "true",
		Records: nil,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer tok")
	s.withAuth(s.handleExec)(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRecipesListAndViewAllowedPath(t *testing.T) {
	tmp := t.TempDir()
	xdg := filepath.Join(tmp, "xdg")
	recipesDir := filepath.Join(xdg, "honey", "recipes")
	if err := os.MkdirAll(recipesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	cuePath := filepath.Join(recipesDir, "webtest.cue")
	content := "package honey\n"
	if err := os.WriteFile(cuePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)

	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "tok",
		Version:    "0",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recipes", nil)
	req.Header.Set("Authorization", "Bearer tok")
	s.withAuth(s.handleRecipesList)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var list recipesListResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Recipes) != 1 {
		t.Fatalf("expected 1 recipe, got %+v", list.Recipes)
	}
	abs := list.Recipes[0].Path

	viewBody, _ := json.Marshal(recipeViewRequest{Path: abs})
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/view", bytes.NewReader(viewBody))
	req2.Header.Set("Authorization", "Bearer tok")
	s.withAuth(s.handleRecipesView)(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("view: expected 200, got %d %s", rec2.Code, rec2.Body.String())
	}
	var vr recipeViewResponse
	if err := json.NewDecoder(rec2.Body).Decode(&vr); err != nil {
		t.Fatal(err)
	}
	if vr.Content != content {
		t.Fatalf("content mismatch: %q", vr.Content)
	}
}
