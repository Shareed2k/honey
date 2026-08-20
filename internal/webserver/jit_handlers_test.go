package webserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
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
	if linkPath != "/?access="+code {
		t.Fatalf("link_path = %q, want /?access=%s", linkPath, code)
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

// createJITGrantDirect creates a grant directly via the store (bypassing the
// HTTP create handler's OPA/notify/tmux plumbing, which is already covered
// elsewhere) so pagination/delete/purge tests can set up fixtures without
// wiring a fake tmux server for every case.
func createJITGrantDirect(t *testing.T, store *jit.Store, name string) jit.Grant {
	t.Helper()
	stored, _, err := store.Create(jit.Grant{
		Actor:        "alice",
		Resource:     jit.ResourceRef{Name: name, Provider: "ssh"},
		Capabilities: []jit.Capability{jit.CapShell},
		Delivery:     jit.DeliveryWeb,
		Duration:     time.Hour,
	})
	if err != nil {
		t.Fatalf("create grant %q: %v", name, err)
	}
	return stored
}

// TestHandleListJITGrants_Pagination table-drives ?page=/?per_page= slicing
// and the total/page/per_page response fields against a fixed 5-grant
// fixture, newest-first (grant4 created last, so it sorts first).
func TestHandleListJITGrants_Pagination(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	var names []string
	for i := 0; i < 5; i++ {
		g := createJITGrantDirect(t, store, fmt.Sprintf("host-%d", i))
		names = append([]string{g.Resource.Name}, names...) // prepend: newest-first order
	}

	tests := []struct {
		name        string
		query       string
		wantNames   []string
		wantTotal   float64
		wantPage    float64
		wantPerPage float64
	}{
		{name: "default page/per_page", query: "", wantNames: names, wantTotal: 5, wantPage: 1, wantPerPage: 50},
		{name: "page 1 size 2", query: "?page=1&per_page=2", wantNames: names[0:2], wantTotal: 5, wantPage: 1, wantPerPage: 2},
		{name: "page 2 size 2", query: "?page=2&per_page=2", wantNames: names[2:4], wantTotal: 5, wantPage: 2, wantPerPage: 2},
		{name: "page 3 size 2 (partial last page)", query: "?page=3&per_page=2", wantNames: names[4:5], wantTotal: 5, wantPage: 3, wantPerPage: 2},
		{name: "page past the end is empty, not an error", query: "?page=99&per_page=2", wantNames: nil, wantTotal: 5, wantPage: 99, wantPerPage: 2},
		{name: "per_page capped at 200", query: "?per_page=99999", wantNames: names, wantTotal: 5, wantPage: 1, wantPerPage: 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := doJSON(t, s, http.MethodGet, "/api/v1/jit/grants"+tc.query, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
			}
			var resp struct {
				Grants  []map[string]any `json:"grants"`
				Total   float64          `json:"total"`
				Page    float64          `json:"page"`
				PerPage float64          `json:"per_page"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Total != tc.wantTotal || resp.Page != tc.wantPage || resp.PerPage != tc.wantPerPage {
				t.Fatalf("total/page/per_page = %v/%v/%v, want %v/%v/%v", resp.Total, resp.Page, resp.PerPage, tc.wantTotal, tc.wantPage, tc.wantPerPage)
			}
			gotNames := make([]string, 0, len(resp.Grants))
			for _, g := range resp.Grants {
				gotNames = append(gotNames, g["resource"].(map[string]any)["name"].(string))
			}
			if len(gotNames) != len(tc.wantNames) {
				t.Fatalf("got %d grants %v, want %d %v", len(gotNames), gotNames, len(tc.wantNames), tc.wantNames)
			}
			for i := range gotNames {
				if gotNames[i] != tc.wantNames[i] {
					t.Fatalf("grant[%d] = %q, want %q (names must stay newest-first)", i, gotNames[i], tc.wantNames[i])
				}
			}
		})
	}
}

func TestHandleDeleteJITGrant_RefusesActiveGrant(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	g := createJITGrantDirect(t, store, "host1")

	w := doJSON(t, s, http.MethodDelete, "/api/v1/jit/grants/"+g.ID, nil)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 body=%s", w.Code, w.Body)
	}
	if _, ok := store.Get(g.ID); !ok {
		t.Fatal("an active grant refused by delete must still exist")
	}
}

func TestHandleDeleteJITGrant_DeletesTerminalGrant(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	g := createJITGrantDirect(t, store, "host1")
	if _, err := store.Revoke(g.ID, "dave"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	w := doJSON(t, s, http.MethodDelete, "/api/v1/jit/grants/"+g.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 body=%s", w.Code, w.Body)
	}
	if _, ok := store.Get(g.ID); ok {
		t.Fatal("deleted grant must be gone")
	}
}

func TestHandleDeleteJITGrant_UnknownID(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	w := doJSON(t, s, http.MethodDelete, "/api/v1/jit/grants/jit_nope", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 body=%s", w.Code, w.Body)
	}
}

func TestHandleJITGrantsPurge_DeletesTerminalGrantsAndReturnsCount(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	active := createJITGrantDirect(t, store, "active-host")
	revoked := createJITGrantDirect(t, store, "revoked-host")
	if _, err := store.Revoke(revoked.ID, "dave"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/purge", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["deleted"] != float64(1) {
		t.Fatalf("deleted = %v, want 1", resp["deleted"])
	}
	if _, ok := store.Get(active.ID); !ok {
		t.Fatal("purge must not touch an active grant")
	}
	if _, ok := store.Get(revoked.ID); ok {
		t.Fatal("purge must remove the revoked grant")
	}
}

func TestHandleJITGrantsPurge_NothingToPurge(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	createJITGrantDirect(t, store, "active-host")

	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants/purge", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", w.Code, w.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["deleted"] != float64(0) {
		t.Fatalf("deleted = %v, want 0", resp["deleted"])
	}
}

// TestJITGrantsMutationEndpoints_NilStore covers the 503 path for the
// pagination/delete/purge endpoints added alongside TestJITEndpoints_NilStore.
func TestJITGrantsMutationEndpoints_NilStore(t *testing.T) {
	s := &Server{opts: Options{AuditSink: audit.NewNoopSink()}}

	t.Run("delete", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/jit/grants/jit_x", nil)
		s.handleDeleteJITGrant(w, r)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body)
		}
	})
	t.Run("purge", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/jit/grants/purge", nil)
		s.handleJITGrantsPurge(w, r)
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

// TestNewServer_JitDisabledByConfig verifies config.Jit.Enabled=false stops
// NewServer from default-constructing a jit.Store at all (unlike
// TestJITEndpoints_NilStore, which constructs a bare *Server to exercise the
// "no state dir" 503 path, this goes through real NewServer/routing since the
// disabling must happen before the state-dir store construction runs).
func TestNewServer_JitDisabledByConfig(t *testing.T) {
	disabled := false
	s := newTestServer(t, Options{Config: &config.File{Jit: &config.JitConfig{Enabled: &disabled}}})
	if s.opts.Jit != nil {
		t.Fatal("expected nil Jit store when config.Jit.Enabled=false")
	}

	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "1h",
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body)
	}
}

// TestHandleCreateJITGrant_MaxDurationConfigCap verifies config.Jit.MaxDuration
// caps a requested duration even when it is well under the package's built-in
// jitMaxDuration (24h) default.
func TestHandleCreateJITGrant_MaxDurationConfigCap(t *testing.T) {
	s, _ := newJitTestServer(t, Options{Config: &config.File{Jit: &config.JitConfig{MaxDuration: "1h"}}})

	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "8h",
	}
	before := time.Now()
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	expRaw, _ := resp["expires_at"].(string)
	if expRaw == "" {
		t.Fatal("expected non-empty expires_at for a direct grant")
	}
	exp, err := time.Parse(time.RFC3339, expRaw)
	if err != nil {
		t.Fatalf("parse expires_at: %v", err)
	}
	if got := exp.Sub(before); got < 55*time.Minute || got > 65*time.Minute {
		t.Fatalf("expires_at ~%s after now, want ~1h (config max_duration cap must win over the 8h request and the 24h built-in default)", got)
	}
}

// TestHandleCreateJITGrant_LinkUsesConcreteListenAddrNotRealResolver is the
// LOW-Q2 regression: newTestServer's ListenAddr must be a concrete,
// non-loopback address so shareBaseURL takes its fast path here — if it were
// still the old loopback default, this handler would fall through to
// defaultLANResolver and make real net.Dial/net.InterfaceAddrs syscalls in
// what is otherwise a hermetic test suite.
func TestHandleCreateJITGrant_LinkUsesConcreteListenAddrNotRealResolver(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "1h",
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	code, _ := resp["code"].(string)
	link, _ := resp["link"].(string)
	if want := "http://203.0.113.10:0/?access=" + code; link != want {
		t.Fatalf("link = %q, want %q (deterministic from the concrete ListenAddr, no real resolver call)", link, want)
	}
}

// jitLiveTerminalBody returns a POST /jit/grants body shaped like the webui's
// "Share this terminal" action: kind/mux_session/capability instead of a
// generic capabilities array.
func jitLiveTerminalBody(muxSession, capability string) map[string]any {
	return map[string]any{
		"kind":        "live_terminal",
		"mux_session": muxSession,
		"capability":  capability,
		"resource":    map[string]any{"name": "op-terminal", "provider": "ssh"},
		"duration":    "1h",
	}
}

// withFakeInterceptSessionActorRetry overrides the interceptSessionActorRetry
// seam for the duration of the calling test, restoring the original on
// cleanup — lets a test drive the MED-3 fail-closed path (unknown owner)
// without waiting out the real bounded retry's sleeps.
func withFakeInterceptSessionActorRetry(t *testing.T, fn func(string) string) {
	t.Helper()
	orig := interceptSessionActorRetry
	interceptSessionActorRetry = fn
	t.Cleanup(func() { interceptSessionActorRetry = orig })
}

// fakeTmuxCanonical returns a swapTmuxRun fake that answers `display-message`
// by echoing back exactly wantCanonical (NEW-3's exact-match resolution) and
// `show-environment` with a HONEY_INT_ACTOR of actor — the two tmux calls
// applyLiveTerminalShare makes for a honey-int-* mux_session. Any other verb
// fails the test loudly rather than silently returning a zero value.
func fakeTmuxCanonical(t *testing.T, wantCanonical, actor string) func() {
	t.Helper()
	fake := func(args ...string) ([]byte, error) {
		if len(args) == 0 {
			t.Fatalf("unexpected empty tmux call")
		}
		switch args[0] {
		case "display-message":
			return []byte(wantCanonical + "\n"), nil
		case "show-environment":
			return []byte("HONEY_INT_ACTOR=" + actor + "\n"), nil
		default:
			t.Fatalf("unexpected tmux args %v", args)
			return nil, nil
		}
	}
	// show-environment (ownership) still goes through the shared tmuxRun;
	// display-message (canonicalization, NEW-15) now goes through the
	// bounded tmuxRunGuest — fake both so either call resolves the same way.
	restoreRun := swapTmuxRun(fake)
	restoreGuest := swapTmuxRunGuest(fake)
	return func() {
		restoreRun()
		restoreGuest()
	}
}

// TestHandleCreateJITGrant_LiveTerminalShare table-drives the grant-create
// side of a live-session share: a valid honey_*/honey-int-* mux_session with
// watch or collaborate succeeds and rewrites the stored grant's
// ResourceRef.Meta + Capabilities + Delivery; a missing/invalid mux_session, a
// bad capability, or (MED-4) a session that isn't actually live is a 400,
// never a silently-accepted grant a guest could later redeem into a broken or
// over-privileged attach. Every case fakes tmuxGuestSessionAlive (see
// pty_proxy_test.go) so this stays hermetic — no real tmux server needed —
// except the one case that specifically exercises MED-4's "not live" 400.
// The two success cases also fake tmuxRun so NEW-3's canonicalization (and,
// for honey-int-*, MED-3's now-fail-closed ownership check) see a real tmux
// session that resolves EXACTLY to the requested name.
func TestHandleCreateJITGrant_LiveTerminalShare(t *testing.T) {
	tests := []struct {
		name       string
		muxSession string
		capability string
		notAlive   bool // MED-4: fake the session as ended instead of live
		wantStatus int
	}{
		{name: "watch on honey_ session", muxSession: "honey_abc123", capability: "watch", wantStatus: http.StatusOK},
		{name: "collaborate on honey-int- session", muxSession: "honey-int-deadbeef", capability: "collaborate", wantStatus: http.StatusOK},
		{name: "empty mux_session rejected", muxSession: "", capability: "watch", wantStatus: http.StatusBadRequest},
		{name: "malformed mux_session rejected", muxSession: "rm -rf /", capability: "watch", wantStatus: http.StatusBadRequest},
		{name: "shell capability rejected for live_terminal", muxSession: "honey_abc123", capability: "shell", wantStatus: http.StatusBadRequest},
		{name: "empty capability rejected", muxSession: "honey_abc123", capability: "", wantStatus: http.StatusBadRequest},
		{name: "no live tmux session rejected", muxSession: "honey_abc123", capability: "watch", notAlive: true, wantStatus: http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFakeGuestSessionAlive(t, !tc.notAlive)
			// api is actorFromCtx's default identity for an unauthenticated
			// doJSON request — matches it so the honey-int- case's MED-3
			// ownership check (now fail-closed) allows.
			defer fakeTmuxCanonical(t, tc.muxSession, "api")()

			s, store := newJitTestServer(t, Options{})
			w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", jitLiveTerminalBody(tc.muxSession, tc.capability))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", w.Code, tc.wantStatus, w.Body)
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			all := store.List()
			if len(all) != 1 {
				t.Fatalf("expected 1 stored grant, got %d", len(all))
			}
			g := all[0]
			if g.Resource.Meta["kind"] != "live_terminal" {
				t.Fatalf("Meta[kind] = %q, want live_terminal", g.Resource.Meta["kind"])
			}
			if g.Resource.Meta["mux_session"] != tc.muxSession {
				t.Fatalf("Meta[mux_session] = %q, want %q", g.Resource.Meta["mux_session"], tc.muxSession)
			}
			if len(g.Capabilities) != 1 || string(g.Capabilities[0]) != tc.capability {
				t.Fatalf("Capabilities = %v, want [%s]", g.Capabilities, tc.capability)
			}
			if g.Delivery != jit.DeliveryWeb {
				t.Fatalf("Delivery = %q, want web (a live-terminal attach only exists over the browser terminal)", g.Delivery)
			}
		})
	}
}

// TestHandleCreateJITGrant_LiveTerminalShare_RejectsPrefixAlias is the NEW-3
// regression: tmux matches a `-t` target by PREFIX, so a request naming a
// unique prefix of a real session would otherwise pass name/liveness/
// ownership checks and attach to the REAL session while the stored grant
// (and later, policy/audit) only ever saw the alias. display-message
// resolving to a DIFFERENT name than what was requested must be rejected
// outright, storing nothing.
func TestHandleCreateJITGrant_LiveTerminalShare_RejectsPrefixAlias(t *testing.T) {
	withFakeGuestSessionAlive(t, true)
	defer fakeTmuxCanonical(t, "honey_alias_the_real_full_session_name", "api")()

	s, store := newJitTestServer(t, Options{})
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", jitLiveTerminalBody("honey_alias", "watch"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body)
	}
	if len(store.List()) != 0 {
		t.Fatal("a prefix-alias mux_session must never be stored")
	}
}

// TestHandleCreateJITGrant_MetaOnlyKindStillValidated is the MED-2 regression:
// a request that sets kind/mux_session only inside resource.meta (never the
// top-level fields) used to skip applyLiveTerminalShare entirely, reaching
// the store with an unvalidated mux_session baked into ResourceRef.Meta —
// exactly what a probe would send to bypass the mux-name gate. It must be
// validated (and here, rejected) the same as the top-level form.
func TestHandleCreateJITGrant_MetaOnlyKindStillValidated(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	body := map[string]any{
		"resource": map[string]any{
			"name": "op-terminal",
			"meta": map[string]any{"kind": "live_terminal", "mux_session": "rm -rf /"},
		},
		"duration": "1h",
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body)
	}
	if len(store.List()) != 0 {
		t.Fatal("a meta-only bypass attempt must never reach the store")
	}
}

// TestHandleCreateJITGrant_LiveTerminalShare_InterceptActorMismatch is the
// MED-3 ownership regression: a live share of a honey-int-* (intercept
// resume) session is refused when its recorded HONEY_INT_ACTOR
// (interceptResumeSetMeta) does not match the requester creating the grant.
// doJSON's requests carry no auth, so the requester is actorFromCtx's default
// ("api"). The unknown-owner case is covered separately, below.
func TestHandleCreateJITGrant_LiveTerminalShare_InterceptActorMismatch(t *testing.T) {
	const mux = "honey-int-actorcheck01"
	tests := []struct {
		name          string
		recordedActor string
		wantStatus    int
	}{
		{name: "different actor refused", recordedActor: "someone-else", wantStatus: http.StatusBadRequest},
		{name: "matching actor allowed", recordedActor: "api", wantStatus: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFakeGuestSessionAlive(t, true)
			defer fakeTmuxCanonical(t, mux, tc.recordedActor)()

			s, _ := newJitTestServer(t, Options{})
			w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", jitLiveTerminalBody(mux, "collaborate"))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", w.Code, tc.wantStatus, w.Body)
			}
		})
	}
}

// TestHandleCreateJITGrant_LiveTerminalShare_InterceptUnknownOwnerFailsClosed
// is the MED-3 round-2 residual: unlike round 1 (which allowed an unknown
// honey-int-* owner), a session whose owner STILL cannot be determined after
// interceptSessionActorRetry's bounded retry must now be refused — those
// names are derivable cross-tenant and the intercept list is visible to any
// authenticated user, so "we don't know" must mean "deny" for this family.
// Fakes the retry seam directly (not tmuxRun) so this test never waits out a
// real retry budget.
func TestHandleCreateJITGrant_LiveTerminalShare_InterceptUnknownOwnerFailsClosed(t *testing.T) {
	withFakeGuestSessionAlive(t, true)
	withFakeInterceptSessionActorRetry(t, func(string) string { return "" })

	s, store := newJitTestServer(t, Options{})
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", jitLiveTerminalBody("honey-int-unknownowner", "watch"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body)
	}
	if len(store.List()) != 0 {
		t.Fatal("a honey-int-* share with an undetermined owner must never be stored")
	}
}

// TestHandleCreateJITGrant_LiveTerminalShare_DeadSessionSkipsOwnershipCheck
// is the NEW-16 regression: round 2 ran the ownership retry BEFORE the
// liveness check, so a dead honey-int-* session paid for the retry's full
// cost (6 execs, ~500ms) and returned "owner could not be determined"
// instead of the friendlier, cheaper "no live tmux session" message.
// interceptSessionActorRetry is faked to fail the test outright if it is
// ever called, proving liveness now short-circuits first.
func TestHandleCreateJITGrant_LiveTerminalShare_DeadSessionSkipsOwnershipCheck(t *testing.T) {
	withFakeGuestSessionAlive(t, false)
	withFakeInterceptSessionActorRetry(t, func(name string) string {
		t.Fatalf("ownership retry must not run before the liveness check (got name %q)", name)
		return ""
	})

	s, store := newJitTestServer(t, Options{})
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", jitLiveTerminalBody("honey-int-deadsession", "watch"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "not shareable") {
		t.Fatalf("body = %s, want the friendly \"not shareable\" liveness message, not an ownership error", w.Body)
	}
	if len(store.List()) != 0 {
		t.Fatal("a dead session must never be stored")
	}
}

// TestHandleCreateJITGrant_OmitsEmptyMuxSessionFromPolicyInput is the NEW-8
// regression: gateJITGrant used to add "mux_session": "" to the OPA target
// unconditionally, changing the input shape for every existing "jit_grant"
// policy (not just live-terminal ones). This policy denies whenever
// mux_session is present in target AT ALL — it must never fire for an
// ordinary, non-live grant.
func TestHandleCreateJITGrant_OmitsEmptyMuxSessionFromPolicyInput(t *testing.T) {
	const src = `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "jit_grant"
	"mux_session" in object.keys(input.target)
}
deny_reason := "mux_session key must be absent when empty" if {
	input.action == "jit_grant"
	"mux_session" in object.keys(input.target)
}`
	enf := mustEnforcer(t, src)
	s, _ := newJitTestServer(t, Options{Enforcer: enf})

	body := map[string]any{
		"resource":     map[string]any{"name": "host1"},
		"capabilities": []string{"shell"},
		"delivery":     "web",
		"duration":     "1h",
	}
	w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s (mux_session key must be omitted, not just empty, for a non-live grant)", w.Code, w.Body)
	}
}
