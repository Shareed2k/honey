package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
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
	s.router.ServeHTTP(rec, req)
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
	s.router.ServeHTTP(rec, req)
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
	payload, _ := json.Marshal(ExecRequest{
		SSHUser: "u",
		Command: "true",
		Records: nil,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer tok")
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestValidateExecRequest_ScriptAndRunAs(t *testing.T) {
	t.Parallel()
	s := &Server{}
	rec := []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}}

	t.Run("valid script mode", func(t *testing.T) {
		mode, err := s.validateExecRequest(ExecRequest{
			SSHUser: "u", Command: "echo hi\n", ExecMode: "script",
			ScriptInterpreter: "bash", ScriptArgs: []string{"a", "b"}, RunAs: "ops", Records: rec,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mode != execModeScript {
			t.Fatalf("mode = %q, want %q", mode, execModeScript)
		}
	})

	t.Run("bad run_as rejected", func(t *testing.T) {
		if _, err := s.validateExecRequest(ExecRequest{
			SSHUser: "u", Command: "true", RunAs: "bad user!", Records: rec,
		}); err == nil {
			t.Fatal("expected error for invalid run_as")
		}
	})

	t.Run("too many script args rejected", func(t *testing.T) {
		args := make([]string, maxWebExecArgs+1)
		if _, err := s.validateExecRequest(ExecRequest{
			SSHUser: "u", Command: "x", ExecMode: "script", ScriptArgs: args, Records: rec,
		}); err == nil {
			t.Fatal("expected error for too many script_args")
		}
	})
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
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var list RecipesListResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Recipes) != 1 {
		t.Fatalf("expected 1 recipe, got %+v", list.Recipes)
	}
	abs := list.Recipes[0].Path

	viewBody, _ := json.Marshal(RecipeViewRequest{Path: abs})
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/view", bytes.NewReader(viewBody))
	req2.Header.Set("Authorization", "Bearer tok")
	s.router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("view: expected 200, got %d %s", rec2.Code, rec2.Body.String())
	}
	var vr RecipeViewResponse
	if err := json.NewDecoder(rec2.Body).Decode(&vr); err != nil {
		t.Fatal(err)
	}
	if vr.Content != content {
		t.Fatalf("content mismatch: %q", vr.Content)
	}
}

func TestHandleCueExec_recipeContent_dryRun(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "tok",
		Version:    "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{
		"recipe_content": {
			"name": "inline-test",
			"steps": [
				{"host": "*", "command": "echo hi"}
			]
		},
		"execute": false,
		"ssh_user": "ops",
		"records": [
			{"provider":"static","name":"h1","primary_ip":"1.1.1.1"}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "echo hi") {
		t.Fatalf("expected plan to mention 'echo hi', got: %s", w.Body)
	}
}

func TestHandleCueExec_recipeContent_invalidHost(t *testing.T) {
	t.Parallel()
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "tok",
		Version:    "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{
		"recipe_content": {
			"name": "bad",
			"steps": [{"host": "re:[", "command": "true"}]
		},
		"execute": false,
		"records": [
			{"provider":"static","name":"h1","primary_ip":"1.1.1.1"}
		]
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cue-exec", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (%s)", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "host") {
		t.Fatalf("expected error message to mention 'host', got: %s", w.Body)
	}
}
