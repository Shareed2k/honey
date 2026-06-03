package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/snippets"
)

func newSnippetServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "tok", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	// Point the store at an isolated temp file.
	s.snippetStore = snippets.NewLocalStore(filepath.Join(t.TempDir(), "snippets.json"))
	return s
}

func snippetReq(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	var r *http.Request
	if body != nil {
		payload, _ := json.Marshal(body)
		r = httptest.NewRequest(method, target, bytes.NewReader(payload))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.Header.Set("Authorization", "Bearer tok")
	return r
}

func TestSnippetsSaveListDelete(t *testing.T) {
	t.Parallel()
	s := newSnippetServer(t)

	// Save
	rec := httptest.NewRecorder()
	s.withAuth(s.handleSnippetsSave)(rec, snippetReq(t, http.MethodPost, "/api/v1/snippets",
		snippets.ExecSnippet{Name: "uptime", Mode: "command", Command: "uptime"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("save: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var saved snippets.ExecSnippet
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("expected generated id")
	}

	// List
	rec = httptest.NewRecorder()
	s.withAuth(s.handleSnippetsList)(rec, snippetReq(t, http.MethodGet, "/api/v1/snippets", nil))
	var list []snippets.ExecSnippet
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != saved.ID {
		t.Fatalf("list mismatch: %+v", list)
	}

	// Delete
	rec = httptest.NewRecorder()
	req := snippetReq(t, http.MethodDelete, "/api/v1/snippets/"+saved.ID, nil)
	req.SetPathValue("id", saved.ID)
	s.withAuth(s.handleSnippetsDelete)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", rec.Code)
	}

	// Delete unknown → 404
	rec = httptest.NewRecorder()
	req = snippetReq(t, http.MethodDelete, "/api/v1/snippets/nope", nil)
	req.SetPathValue("id", "nope")
	s.withAuth(s.handleSnippetsDelete)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown: want 404, got %d", rec.Code)
	}
}

func TestSnippetsSaveValidation(t *testing.T) {
	t.Parallel()
	s := newSnippetServer(t)
	cases := []snippets.ExecSnippet{
		{Name: "", Mode: "command", Command: "x"},                      // empty name
		{Name: "a", Mode: "bogus", Command: "x"},                       // bad mode
		{Name: "a", Mode: "command", Command: ""},                      // empty command
		{Name: "a", Mode: "command", Command: "x", RunAs: "bad user!"}, // bad run_as
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		s.withAuth(s.handleSnippetsSave)(rec, snippetReq(t, http.MethodPost, "/api/v1/snippets", tc))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("want 400 for %+v, got %d", tc, rec.Code)
		}
	}
}
