package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/jit"
)

// shareWatchGoleakOpts is the standard goleak allowlist for a handleShareWatch
// e2e test: httptest.NewServer's own background pool/cache maintenance loops
// have no Shutdown path and legitimately outlive any one test (see
// runHandleWebInteractiveStreams's own note in termguard_wire_test.go); only
// the handler's per-connection goroutines (including WATCHFIT-1's size
// poller) are checked.
func shareWatchGoleakOpts(t *testing.T) []goleak.Option {
	t.Helper()
	return []goleak.Option{
		goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).ticktock"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).purgeStaleWorkers"),
		goleak.IgnoreTopFunction("github.com/jellydator/ttlcache/v3.(*Cache[...]).Start"),
	}
}

// readShareWatchSizeFrame reads WS messages from conn until it finds a
// {"size":{...}} control frame, and returns its cols/rows. Fails the test if
// none arrives before conn's own read deadline.
func readShareWatchSizeFrame(t *testing.T, conn *websocket.Conn) (cols, rows int) {
	t.Helper()
	for {
		mt, payload, err := conn.ReadMessage()
		require.NoError(t, err)
		if mt != websocket.TextMessage {
			continue
		}
		var frame struct {
			Size struct {
				Cols int `json:"cols"`
				Rows int `json:"rows"`
			} `json:"size"`
		}
		if json.Unmarshal(payload, &frame) == nil && frame.Size.Cols > 0 && frame.Size.Rows > 0 {
			return frame.Size.Cols, frame.Size.Rows
		}
	}
}

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

// TestHandleShareWatch_SizesObserverToGuestWindow is the WATCHFIT-1
// regression: measured on a live session, an observer's pty was left at the
// creack/pty default (80x24) instead of the guest's actual window size, so
// tmux drew a truncated 80x24 viewport onto a much larger window. The guest's
// window here (201x57) is deliberately far from that default, so a
// regression back to "just use the pty default" can't accidentally pass.
func TestHandleShareWatch_SizesObserverToGuestWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; share watch requires it")
	}

	store, err := jit.NewStore(t.TempDir()+"/jit_grants.jsonl", nil)
	require.NoError(t, err)
	g := createJITGrantDirect(t, store, "watch-size-target")
	mux := shareGuestMuxName(g.ID)

	require.True(t, validHoneyMuxSessionName(mux))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", mux).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", mux, "-x", "201", "-y", "57", "--", "cat").Run())

	s := newTestServer(t, Options{Jit: store})
	ts := httptest.NewServer(s.router)
	defer ts.Close()
	defer goleak.VerifyNone(t, shareWatchGoleakOpts(t)...)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/share/watch?grant=" + g.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))

	// The very first control frame must be the guest's REAL window size —
	// never the hello's 80x24 the observer's own client claimed (which the
	// server ignores entirely, per handleShareWatch's own doc).
	cols, rows := readShareWatchSizeFrame(t, conn)
	require.Equal(t, 201, cols)
	require.Equal(t, 57, rows)

	// The observer's OWN tmux client must actually be sized to match — not
	// just the WS frame the browser received.
	require.Eventually(t, func() bool {
		out, cerr := exec.Command("tmux", "list-clients", "-t", mux, "-F", "#{client_width}x#{client_height}").Output()
		return cerr == nil && strings.TrimSpace(string(out)) == "201x57"
	}, 3*time.Second, 50*time.Millisecond, "observer's pty must be sized to the guest's window, not left at the pty default")

	require.NoError(t, conn.Close())
	require.Eventually(t, func() bool {
		return exec.Command("tmux", "has-session", "-t", mux).Run() == nil
	}, 3*time.Second, 50*time.Millisecond, "guest's session must survive an observer disconnect")
}

// TestHandleShareWatch_TracksGuestResizeReadOnly is the WATCHFIT-1 mid-session
// half: a guest resizing after the observer already attached must reach the
// observer (both the browser's size frame and the observer's own pty), and
// the mechanism that makes that happen — the bounded poller — must NEVER
// issue anything but the read-only window-size query. That second half is the
// load-bearing security assertion: an observer's size sync can never be the
// thing that resizes (or otherwise touches) the guest's window.
func TestHandleShareWatch_TracksGuestResizeReadOnly(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; share watch requires it")
	}

	origInterval := shareWatchSizePollInterval
	shareWatchSizePollInterval = 30 * time.Millisecond
	t.Cleanup(func() { shareWatchSizePollInterval = origInterval })

	store, err := jit.NewStore(t.TempDir()+"/jit_grants.jsonl", nil)
	require.NoError(t, err)
	g := createJITGrantDirect(t, store, "watch-resize-target")
	mux := shareGuestMuxName(g.ID)

	require.True(t, validHoneyMuxSessionName(mux))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", mux).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", mux, "-x", "80", "-y", "24", "--", "cat").Run())

	// Wrap (never replace) the real bounded runner: the poller must keep
	// actually working, while every argv it ever issues is captured for the
	// assertion below.
	origRunGuest := tmuxRunGuest
	var mu sync.Mutex
	var calls [][]string
	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, append([]string(nil), args...))
		mu.Unlock()
		return origRunGuest(args...)
	})
	defer restore()

	s := newTestServer(t, Options{Jit: store})
	ts := httptest.NewServer(s.router)
	defer ts.Close()
	defer goleak.VerifyNone(t, shareWatchGoleakOpts(t)...)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/share/watch?grant=" + g.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))

	cols, rows := readShareWatchSizeFrame(t, conn)
	require.Equal(t, 80, cols)
	require.Equal(t, 24, rows)

	// The guest resizes mid-session (e.g. its own terminal window changed).
	require.NoError(t, exec.Command("tmux", "resize-window", "-t", mux, "-x", "120", "-y", "40").Run())

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	cols, rows = readShareWatchSizeFrame(t, conn)
	require.Equal(t, 120, cols)
	require.Equal(t, 40, rows)

	require.Eventually(t, func() bool {
		out, cerr := exec.Command("tmux", "list-clients", "-t", mux, "-F", "#{client_width}x#{client_height}").Output()
		return cerr == nil && strings.TrimSpace(string(out)) == "120x40"
	}, 3*time.Second, 20*time.Millisecond, "observer's pty must track the guest's resize")

	require.NoError(t, conn.Close())
	require.Eventually(t, func() bool {
		return exec.Command("tmux", "has-session", "-t", mux).Run() == nil
	}, 3*time.Second, 50*time.Millisecond, "guest's session must survive an observer disconnect")

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, calls, "expected the size sync to have queried tmux at least once")
	for _, args := range calls {
		require.NotEmpty(t, args)
		require.Equal(t, "display", args[0],
			"an observer's size sync must only ever query, never mutate, the guest's window; got argv %v", args)
	}
}

// TestHandleShareWatch_ObserverDisconnectDetachesOwnClientOnly is the
// WATCHFIT-2 regression: measured live, two read-only clients were still
// attached from watch sessions the operator had already closed — the
// panel's Observers count only ever grew. A plain disconnect (the modal
// closing its WebSocket, no close_tab frame) must make the observer's OWN
// tmux client go away, while the guest's session survives untouched and no
// kill-session is ever issued (that would also drop the guest, not just this
// observer).
func TestHandleShareWatch_ObserverDisconnectDetachesOwnClientOnly(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; share watch requires it")
	}

	store, err := jit.NewStore(t.TempDir()+"/jit_grants.jsonl", nil)
	require.NoError(t, err)
	g := createJITGrantDirect(t, store, "watch-detach-target")
	mux := shareGuestMuxName(g.ID)

	require.True(t, validHoneyMuxSessionName(mux))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", mux).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", mux, "--", "cat").Run())

	origRunGuest := tmuxRunGuest
	var mu sync.Mutex
	var calls [][]string
	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, append([]string(nil), args...))
		mu.Unlock()
		return origRunGuest(args...)
	})
	defer restore()

	s := newTestServer(t, Options{Jit: store})
	ts := httptest.NewServer(s.router)
	defer ts.Close()
	defer goleak.VerifyNone(t, shareWatchGoleakOpts(t)...)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws/share/watch?grant=" + g.ID
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	require.NoError(t, conn.WriteJSON(map[string]int{"cols": 80, "rows": 24}))
	_, _ = readShareWatchSizeFrame(t, conn)

	// The spawned "tmux attach -r" client still needs to finish connecting to
	// the tmux server after pty.StartWithSize returns (fork+exec is not
	// "already registered as a client") — eventually, not immediately, same
	// as every other tmux-external-state check in this file.
	require.Eventually(t, func() bool {
		observers, alive := shareObserverCount(mux)
		return alive && observers == 1
	}, 3*time.Second, 20*time.Millisecond, "the observer's own client must be attached before the disconnect")

	// A plain disconnect: exactly what the modal's cleanup does (ws.close()),
	// never an explicit close_tab control frame.
	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		observers, alive := shareObserverCount(mux)
		return alive && observers == 0
	}, 3*time.Second, 50*time.Millisecond, "the observer's own tmux client must be detached after a plain disconnect")

	require.NoError(t, exec.Command("tmux", "has-session", "-t", mux).Run(), "guest's session must survive an observer disconnect")

	mu.Lock()
	defer mu.Unlock()
	for _, args := range calls {
		require.NotEmpty(t, args)
		require.NotEqual(t, "kill-session", args[0], "an observer's own teardown must never kill-session the guest's session; got argv %v", args)
	}
}
