package webserver

import (
	"encoding/json"
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

// TestHandleCreateJITGrant_LiveTerminalShare table-drives the grant-create
// side of a live-session share: a valid honey_*/honey-int-* mux_session with
// watch or collaborate succeeds and rewrites the stored grant's
// ResourceRef.Meta + Capabilities + Delivery; a missing/invalid mux_session, a
// bad capability, or (MED-4) a session that isn't actually live is a 400,
// never a silently-accepted grant a guest could later redeem into a broken or
// over-privileged attach. Every case fakes tmuxGuestSessionAlive (see
// pty_proxy_test.go) so this stays hermetic — no real tmux server needed —
// except the one case that specifically exercises MED-4's "not live" 400.
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
// ("api"). An unrecorded/unknown owner (the plain "collaborate on
// honey-int-" case in TestHandleCreateJITGrant_LiveTerminalShare, which fakes
// no tmuxRun at all) must NOT be treated as a mismatch — that is covered
// there, not here.
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
			restore := swapTmuxRun(func(...string) ([]byte, error) {
				return []byte("HONEY_INT_ACTOR=" + tc.recordedActor + "\n"), nil
			})
			defer restore()

			s, _ := newJitTestServer(t, Options{})
			w := doJSON(t, s, http.MethodPost, "/api/v1/jit/grants", jitLiveTerminalBody(mux, "collaborate"))
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", w.Code, tc.wantStatus, w.Body)
			}
		})
	}
}
