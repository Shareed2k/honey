package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/jit"
)

// newJitTestServer builds a *Server wired with a jit.Store backed by a file
// under t.TempDir(), using the shared newTestServer harness (see opa_test.go)
// so auth/actor resolution matches the rest of the package's tests.
func newJitTestServer(t *testing.T, opts Options) (*Server, *jit.Store) {
	t.Helper()
	store, err := jit.NewStore(filepath.Join(t.TempDir(), "jit_grants.jsonl"), nil)
	if err != nil {
		t.Fatalf("jit.NewStore: %v", err)
	}
	opts.Jit = store
	return newTestServer(t, opts), store
}

func doJSON(t *testing.T, s *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = httptest.NewRequest(method, path, strings.NewReader(string(b)))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, r)
	return w
}

func TestHandleCreateJITGrant_DirectGrant(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":     map[string]any{"name": "host1", "provider": "ssh"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "2h",
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != string(jit.StatusApproved) {
		t.Fatalf("status = %v, want approved", resp["status"])
	}
	code, _ := resp["code"].(string)
	if code == "" {
		t.Fatal("expected non-empty code")
	}
	linkPath, _ := resp["link_path"].(string)
	if linkPath != "/access/"+code {
		t.Fatalf("link_path = %q, want /access/%s", linkPath, code)
	}
	if exp, _ := resp["expires_at"].(string); exp == "" {
		t.Fatal("expected non-empty expires_at for a direct grant")
	}
	if resp["require_approval"] != false {
		t.Fatalf("require_approval = %v, want false", resp["require_approval"])
	}
	if resp["id"] == "" || resp["id"] == nil {
		t.Fatal("expected non-empty id")
	}
}

func TestHandleCreateJITGrant_RequireApproval(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":         map[string]any{"name": "host1"},
		"capabilities":     []string{"shell"},
		"delivery":         "web",
		"duration":         "1h",
		"require_approval": true,
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["status"] != string(jit.StatusPending) {
		t.Fatalf("status = %v, want pending", resp["status"])
	}
	if v, ok := resp["expires_at"]; ok && v != "" && v != nil {
		t.Fatalf("expected no expires_at for a pending grant, got %v", v)
	}
	if resp["require_approval"] != true {
		t.Fatalf("require_approval = %v, want true", resp["require_approval"])
	}
}

func TestHandleCreateJITGrant_ValidationError(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{},
		"delivery":     "web",
		"duration":     "1h",
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body)
	}
}

func TestHandleListJITGrants_OmitsCodeHashAndCode(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "1h",
	}
	create := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if create.Code != http.StatusOK {
		t.Fatalf("create failed: %d body=%s", create.Code, create.Body)
	}
	var createResp map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	plainCode, _ := createResp["code"].(string)
	if plainCode == "" {
		t.Fatal("expected non-empty code from create")
	}

	w := doJSON(t, s, http.MethodGet, "/api/v1/jit/grants", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	raw := w.Body.String()
	if strings.Contains(raw, "code_hash") {
		t.Fatalf("list response leaks code_hash: %s", raw)
	}
	if strings.Contains(raw, plainCode) {
		t.Fatalf("list response leaks plaintext code: %s", raw)
	}

	var resp struct {
		Grants []map[string]any `json:"grants"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(resp.Grants) != 1 {
		t.Fatalf("expected 1 grant, got %d", len(resp.Grants))
	}
	if _, ok := resp.Grants[0]["code_hash"]; ok {
		t.Fatal("grant view must not have a code_hash key")
	}

	// Sanity: the store itself does hold the hash (so we know the redaction is
	// a serialization concern, not an accidental empty CodeHash).
	all := store.List()
	if len(all) != 1 || all[0].CodeHash == "" {
		t.Fatal("expected the underlying store to retain the code hash")
	}
}

func TestHandleDecideJITGrant_Approve(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":         map[string]any{"name": "host1"},
		"capabilities":     []string{"shell"},
		"delivery":         "web",
		"duration":         "1h",
		"require_approval": true,
	}
	create := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	var createResp map[string]any
	if err := json.Unmarshal(create.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := createResp["id"].(string)
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/"+id, map[string]any{"decision": "approve"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var decided map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decided); err != nil {
		t.Fatalf("decode decide response: %v", err)
	}
	if decided["status"] != string(jit.StatusApproved) {
		t.Fatalf("status = %v, want approved", decided["status"])
	}
}

func TestHandleDecideJITGrant_MissingID(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/jit_doesnotexist", map[string]any{"decision": "approve"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", w.Code, w.Body)
	}
}

func TestHandleDecideJITGrant_AlreadyDecided(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":         map[string]any{"name": "host1"},
		"capabilities":     []string{"shell"},
		"delivery":         "web",
		"duration":         "1h",
		"require_approval": true,
	}
	create := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	var createResp map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &createResp)
	id, _ := createResp["id"].(string)

	first := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/"+id, map[string]any{"decision": "approve"})
	if first.Code != http.StatusOK {
		t.Fatalf("first decision expected 200, got %d body=%s", first.Code, first.Body)
	}
	second := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/"+id, map[string]any{"decision": "approve"})
	if second.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", second.Code, second.Body)
	}
}

func TestHandleDecideJITGrant_Revoke(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "1h",
	}
	create := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	var createResp map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &createResp)
	id, _ := createResp["id"].(string)

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/"+id, map[string]any{"decision": "revoke"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var decided map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &decided); err != nil {
		t.Fatalf("decode decide response: %v", err)
	}
	if decided["status"] != string(jit.StatusRevoked) {
		t.Fatalf("status = %v, want revoked", decided["status"])
	}
}

func TestHandleDecideJITGrant_UnknownDecision(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "1h",
	}
	create := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	var createResp map[string]any
	_ = json.Unmarshal(create.Body.Bytes(), &createResp)
	id, _ := createResp["id"].(string)

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/"+id, map[string]any{"decision": "bogus"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", w.Code, w.Body)
	}
}

// TestJITEndpoints_NilStore exercises the handlers directly (not through
// NewServer/the router) because NewServer default-constructs a real jit.Store
// whenever config.ResolveStateDir() succeeds — which it does on any machine
// with a resolvable home directory. This mirrors how device_enroll_test.go and
// ssh_enroll_test.go test their own "no state dir" 503 path: construct the
// dependency as nil directly rather than fighting NewServer's defaulting.
func TestJITEndpoints_NilStore(t *testing.T) {
	s := &Server{opts: Options{AuditSink: audit.NewNoopSink()}}

	t.Run("create", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/jit/grants", strings.NewReader(`{}`))
		s.handleCreateJITGrant(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body)
		}
	})
	t.Run("list", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/jit/grants", nil)
		s.handleListJITGrants(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body)
		}
	})
	t.Run("decide", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/jit/grants/jit_x", strings.NewReader(`{"decision":"approve"}`))
		s.handleDecideJITGrant(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body)
		}
	})
}

func TestHandleCreateJITGrant_OPADeny(t *testing.T) {
	const src = `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if { input.action == "jit_grant" }
deny_reason := "no jit grants allowed" if { input.action == "jit_grant" }`
	enf := mustEnforcer(t, src)
	s, _ := newJitTestServer(t, Options{Enforcer: enf})

	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "1h",
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body)
	}
}
