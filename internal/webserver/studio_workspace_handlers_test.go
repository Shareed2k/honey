package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/webserver/workspacestore"
)

type fakeWorkspaceStore struct {
	saved   workspacestore.Workspace
	saveErr error
	loadErr error
}

func (f *fakeWorkspaceStore) Load(_ context.Context) (workspacestore.Workspace, error) {
	return f.saved, f.loadErr
}

func (f *fakeWorkspaceStore) Save(_ context.Context, ws workspacestore.Workspace) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved = ws
	return nil
}

func newTestServerWithStore(t *testing.T, st workspaceStore) *Server {
	t.Helper()
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0", Token: "secret", Version: "0",
		Config: &config.File{Defaults: config.Defaults{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.workspace = st
	return s
}

func do(s *Server, method string, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/v1/studio/workspace", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	s.router.ServeHTTP(rec, req)
	return rec
}

func TestPutStudioWorkspaceRoundTrip(t *testing.T) {
	st := &fakeWorkspaceStore{}
	s := newTestServerWithStore(t, st)
	body := []byte(`{"layout":{"k":1},"openRecipes":["a.cue"],"active":"a.cue"}`)
	rec := do(s, http.MethodPut, body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}
	if st.saved.Active != "a.cue" || len(st.saved.OpenRecipes) != 1 {
		t.Fatalf("stored = %+v", st.saved)
	}
	get := do(s, http.MethodGet, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("GET = %d", get.Code)
	}
	var out workspacestore.Workspace
	_ = json.Unmarshal(get.Body.Bytes(), &out)
	if out.Active != "a.cue" {
		t.Fatalf("GET body active = %q", out.Active)
	}
}

func TestPutStudioWorkspaceMalformed400(t *testing.T) {
	s := newTestServerWithStore(t, &fakeWorkspaceStore{})
	rec := do(s, http.MethodPut, []byte(`{not json`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed PUT = %d", rec.Code)
	}
}

func TestPutStudioWorkspaceTooManyRecipes400(t *testing.T) {
	s := newTestServerWithStore(t, &fakeWorkspaceStore{})
	names := make([]string, 65)
	for i := range names {
		names[i] = "r.cue"
	}
	b, _ := json.Marshal(workspacestore.Workspace{Layout: json.RawMessage(`{}`), OpenRecipes: names})
	rec := do(s, http.MethodPut, b)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("too many recipes PUT = %d", rec.Code)
	}
}

func TestPutStudioWorkspaceOversize400(t *testing.T) {
	s := newTestServerWithStore(t, &fakeWorkspaceStore{})
	big := make([]byte, (256<<10)+1024)
	for i := range big {
		big[i] = 'a'
	}
	body := []byte(`{"layout":"` + string(big) + `"}`)
	rec := do(s, http.MethodPut, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize PUT = %d (want 400)", rec.Code)
	}
}

func TestPutStudioWorkspaceStoreError500Generic(t *testing.T) {
	s := newTestServerWithStore(t, &fakeWorkspaceStore{saveErr: errors.New("disk on fire secret-path /etc/x")})
	rec := do(s, http.MethodPut, []byte(`{"layout":{},"openRecipes":[]}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("store-error PUT = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "secret-path") {
		t.Fatalf("internal error leaked to client: %s", rec.Body.String())
	}
}

func TestStudioWorkspaceRequiresAuth(t *testing.T) {
	s := newTestServerWithStore(t, &fakeWorkspaceStore{})

	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/studio/workspace", nil) // no token
	s.router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusUnauthorized {
		t.Fatalf("GET without auth = %d, want %d", getRec.Code, http.StatusUnauthorized)
	}

	putRec := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/studio/workspace", bytes.NewReader([]byte(`{"layout":{},"openRecipes":[]}`))) // no token
	s.router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusUnauthorized {
		t.Fatalf("PUT without auth = %d, want %d", putRec.Code, http.StatusUnauthorized)
	}
}

func TestPutStudioWorkspaceMaxRecipesAccepted204(t *testing.T) {
	s := newTestServerWithStore(t, &fakeWorkspaceStore{})
	names := make([]string, 64)
	for i := range names {
		names[i] = "r.cue"
	}
	b, _ := json.Marshal(workspacestore.Workspace{Layout: json.RawMessage(`{}`), OpenRecipes: names})
	rec := do(s, http.MethodPut, b)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("PUT with 64 openRecipes = %d, want %d: %s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}
