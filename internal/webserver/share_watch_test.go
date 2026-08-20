package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/jit"
)

func TestHandleShareWatch_Unauthorized(t *testing.T) {
	s := newTestServer(t, Options{Token: "secret-token"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ws/share/watch?grant=jit_x", nil)
	s.handleShareWatch(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleShareWatch_UnknownGrant404(t *testing.T) {
	s, _ := newJitTestServer(t, Options{})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ws/share/watch?grant=jit_nope", nil)
	s.handleShareWatch(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleShareWatch_RefusesNonWebGrant(t *testing.T) {
	s, store := newJitTestServer(t, Options{})
	g, _, err := store.Create(jit.Grant{
		Actor:        "alice",
		Resource:     jit.ResourceRef{Name: "cert-host", Provider: "ssh"},
		Capabilities: []jit.Capability{jit.CapExec},
		Delivery:     jit.DeliveryCert,
		Duration:     time.Hour,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/ws/share/watch?grant="+g.ID, nil)
	s.handleShareWatch(w, r)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestHandleShareWatch_ReadOnlyLiveView proves the full route end to end
// against a REAL tmux session standing in for a guest's redeemed
// access-request shell: the operator's watch attaches read-only (sees the
// guest's live output), never wires a single byte of input into the pane
// (verified via tmux capture-pane, not just the client-side contract), and a
// plain disconnect leaves the guest's session running.
func TestHandleShareWatch_ReadOnlyLiveView(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; share watch requires it")
	}

	store, err := jit.NewStore(t.TempDir()+"/jit_grants.jsonl", nil)
	require.NoError(t, err)
	g := createJITGrantDirect(t, store, "watch-target")
	mux := shareGuestMuxName(g.ID)

	require.True(t, validHoneyMuxSessionName(mux))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", mux).Run() })
	// A trivial producer stands in for the guest's own live shell: it proves
	// content flows from the GRANTED session without needing real keystrokes.
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", mux, "--",
		"sh", "-c", "while :; do echo tick; sleep 0.05; done").Run())

	s := newTestServer(t, Options{Jit: store})
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	// Snapshot taken after NewServer + httptest started (its background
	// pool/cache maintenance loops have no Shutdown path and legitimately
	// outlive any one test — see runHandleWebInteractiveStreams's own note in
	// termguard_wire_test.go); only the handler's per-connection goroutines
	// are checked below.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).ticktock"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).purgeStaleWorkers"),
		goleak.IgnoreTopFunction("github.com/jellydator/ttlcache/v3.(*Cache[...]).Start"),
	)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/share/watch?grant=" + g.ID
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))

	sawTick := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !sawTick {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		mt, payload, rerr := conn.ReadMessage()
		require.NoError(t, rerr)
		if mt == websocket.BinaryMessage && strings.Contains(string(payload), "tick") {
			sawTick = true
		}
	}
	require.True(t, sawTick, "expected to see output from the guest's session")

	// The load-bearing negative: an observer's input must never reach the
	// pane. Send a distinctive marker as both a binary frame and a resize
	// control frame, then confirm neither ever shows up in the pane content —
	// not merely that the client-side contract dropped it, but that the
	// server never wired it into tmux at all.
	const marker = "OBSERVER-INPUT-MUST-NEVER-LAND"
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte(marker+"\n")))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":200,"rows":60}`)))
	time.Sleep(300 * time.Millisecond)

	captured, cerr := exec.Command("tmux", "capture-pane", "-p", "-t", mux).Output()
	require.NoError(t, cerr)
	require.NotContains(t, string(captured), marker, "an observer's input must never reach the guest's pane")

	require.NoError(t, conn.Close())

	// The GUEST's session must survive a plain observer disconnect.
	require.Eventually(t, func() bool {
		return exec.Command("tmux", "has-session", "-t", mux).Run() == nil
	}, 3*time.Second, 50*time.Millisecond, "guest's session must survive an observer disconnect")
}

// TestHandleShareWatch_CloseTabNeverKillsGuestSession proves the explicit ×
// close_tab path — distinct from a plain disconnect — also never touches the
// guest's session: only the observer's own read-only tmux client is reaped.
func TestHandleShareWatch_CloseTabNeverKillsGuestSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; share watch requires it")
	}

	store, err := jit.NewStore(t.TempDir()+"/jit_grants.jsonl", nil)
	require.NoError(t, err)
	g := createJITGrantDirect(t, store, "closetab-target")
	mux := shareGuestMuxName(g.ID)

	require.True(t, validHoneyMuxSessionName(mux))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", mux).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", mux, "--", "cat").Run())

	s := newTestServer(t, Options{Jit: store})
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	defer goleak.VerifyNone(t, goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).ticktock"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).purgeStaleWorkers"),
		goleak.IgnoreTopFunction("github.com/jellydator/ttlcache/v3.(*Cache[...]).Start"),
	)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/share/watch?grant=" + g.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"close_tab"}`)))
	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		return exec.Command("tmux", "has-session", "-t", mux).Run() == nil
	}, 3*time.Second, 50*time.Millisecond, "guest's session must survive an observer close_tab")
}

// TestHandleJITRedeemTerminal_MuxPathReadWriteObservableAndKillable is the
// end-to-end proof that Part 1 and Part 2 actually connect: a redeemed
// access-request grant, with a multiplexer available, attaches the GUEST
// read-write to its own deterministic session (a write reaches the pane and
// echoes back — proof of read-write, not just an argv assertion), is
// reported by /api/v1/share/sessions as session_alive+observable, and
// POST .../kill both revokes the grant and terminates that exact session
// (never anything else). The pre-existing session here makes
// ptyMuxTmuxCommand take its "attach -d to existing session" branch, so no
// subprocess is spawned (see newJitWSTestServer's own doc for why that
// matters in a `go test` binary).
func TestHandleJITRedeemTerminal_MuxPathReadWriteObservableAndKillable(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; the mux redeem path requires it")
	}
	withShareMuxAvailable(t, true)

	store, err := jit.NewStore(t.TempDir()+"/jit_grants.jsonl", nil)
	require.NoError(t, err)
	created, code := createWebGrant(t, store, jit.Grant{Resource: jit.ResourceRef{Name: "mux-e2e-target", Provider: "ssh"}})
	mux := shareGuestMuxName(created.ID)

	require.True(t, validHoneyMuxSessionName(mux))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", mux).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", mux, "--", "cat").Run())

	s := newTestServer(t, Options{Jit: store, RecordDir: t.TempDir(), ExecRegistry: fakeExecRegistry{ex: fakeInteractiveExecutor{}}})
	ts := httptest.NewServer(s.router)
	defer ts.Close()

	wsBase := strings.Replace(ts.URL, "http", "ws", 1)
	conn, resp, err := websocket.DefaultDialer.Dial(wsBase+"/api/v1/jit/redeem/"+code+"/terminal", nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("guest-write\r")))

	sawEcho := false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !sawEcho {
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		mt, payload, rerr := conn.ReadMessage()
		require.NoError(t, rerr)
		if mt == websocket.BinaryMessage && strings.Contains(string(payload), "guest-write") {
			sawEcho = true
		}
	}
	require.True(t, sawEcho, "the guest must be the read-write client of its own session")

	// /api/v1/share/sessions must report this exact session as alive and
	// observable.
	w := doJSON(t, s, http.MethodGet, "/api/v1/share/sessions", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var listResp struct {
		Sessions []shareSessionView `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listResp))
	require.Len(t, listResp.Sessions, 1)
	require.Equal(t, created.ID, listResp.Sessions[0].GrantID)
	require.True(t, listResp.Sessions[0].SessionAlive)
	require.True(t, listResp.Sessions[0].Observable)

	// Kill must revoke the grant AND terminate exactly this session.
	killW := doJSON(t, s, http.MethodPost, "/api/v1/share/sessions/"+created.ID+"/kill", nil)
	require.Equal(t, http.StatusOK, killW.Code)
	var killResp shareKillResponse
	require.NoError(t, json.Unmarshal(killW.Body.Bytes(), &killResp))
	require.True(t, killResp.SessionKilled)

	gotGrant, ok := store.Get(created.ID)
	require.True(t, ok)
	require.Equal(t, jit.StatusRevoked, gotGrant.Status)
	require.Error(t, exec.Command("tmux", "has-session", "-t", mux).Run(), "kill must actually terminate the guest's session")
}
