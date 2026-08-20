package webserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/termguard"
)

// TestInterceptPaneMuxName_Stable proves the resume-path mux name is a pure,
// deterministic function of cluster/namespace/pod: unchanged across calls
// with the same inputs, distinct per pod, and always honey-int- prefixed so a
// later task (the tmux registry) can list active panes by that prefix.
func TestInterceptPaneMuxName_Stable(t *testing.T) {
	a := interceptPaneMuxName("stg2", "argocd", "api-0")
	b := interceptPaneMuxName("stg2", "argocd", "api-0")
	require.Equal(t, a, b)
	require.NotEqual(t, a, interceptPaneMuxName("stg2", "argocd", "api-1"))
	require.NotEqual(t, a, interceptPaneMuxName("stg2", "other-ns", "api-0"))
	require.NotEqual(t, a, interceptPaneMuxName("other-cluster", "argocd", "api-0"))
	require.True(t, strings.HasPrefix(a, "honey-int-"), "got %q", a)
}

// TestPtyProxyExecArgs_Subcommand proves ptyProxyExecArgs builds the
// intercept-pane pane argv in order: binary, subcommand, --config <path>,
// then the base64 payload last.
func TestPtyProxyExecArgs_Subcommand(t *testing.T) {
	args := ptyProxyExecArgs("intercept-pane", "/usr/local/bin/honey", "/etc/honey/config.yaml", "BASE64PAYLOAD==")
	require.Equal(t, []string{
		"/usr/local/bin/honey", "intercept-pane", "--config", "/etc/honey/config.yaml", "BASE64PAYLOAD==",
	}, args)
}

// TestPtyProxyExecArgs_PtyProxyUnchanged proves the existing pty-proxy pane
// argv shape survives the sub-parameter generalization, including the
// no-config-path case (no --config pair emitted).
func TestPtyProxyExecArgs_PtyProxyUnchanged(t *testing.T) {
	args := ptyProxyExecArgs("pty-proxy", "/usr/local/bin/honey", "", "BASE64PAYLOAD==")
	require.Equal(t, []string{"/usr/local/bin/honey", "pty-proxy", "BASE64PAYLOAD=="}, args)
}

// TestPtyMuxTmuxCommand_ExclusiveUnchanged is the regression safety net named
// in the task brief: every pre-existing caller of ptyMuxTmuxCommand passes
// attachExclusive and must see byte-identical argv. A definitely-nonexistent
// session name (no real tmux needed — a missing session, or even a missing
// tmux binary, both resolve tmuxSessionAlive/tmuxHasSession to false, same as
// TestTmuxSessionAlive_exitedPane in pty_mux_test.go) exercises the
// attach-or-create fallback: `new-session -A -D -s <name> <proxyArgs...>`.
func TestPtyMuxTmuxCommand_ExclusiveUnchanged(t *testing.T) {
	name := "honey_exclusive_nonexistent_test_session"
	proxyArgs := []string{"/usr/local/bin/honey", "pty-proxy", "PAYLOAD"}

	cmd, muxName, useZellij, err := ptyMuxTmuxCommand(name, proxyArgs, attachExclusive)
	require.NoError(t, err)
	require.Equal(t, name, muxName)
	require.False(t, useZellij)
	require.Equal(t, []string{"tmux", "new-session", "-A", "-D", "-s", name, "/usr/local/bin/honey", "pty-proxy", "PAYLOAD"}, cmd.Args)
}

// withFakeGuestSessionAlive overrides the tmuxGuestSessionAlive seam for the
// duration of the calling test, restoring the original on cleanup — the
// package's fake-runner seam for guest-attach argv tests that must not depend
// on a real tmux server.
func withFakeGuestSessionAlive(t *testing.T, alive bool) {
	t.Helper()
	orig := tmuxGuestSessionAlive
	tmuxGuestSessionAlive = func(string) bool { return alive }
	t.Cleanup(func() { tmuxGuestSessionAlive = orig })
}

// TestPtyMuxTmuxCommand_GuestAttachArgv table-drives the exact argv shape per
// guest attachMode against a live (faked) session: HIGH-1 requires BOTH modes
// to attach read-only (`attach -r -t <name>`, no -d) — a guest client must
// never hold a mutating tmux client, whether it is "watch" or "collaborate".
// Neither ever builds a new-session argv — ptyMuxTmuxGuestAttach has no such
// branch to begin with.
func TestPtyMuxTmuxCommand_GuestAttachArgv(t *testing.T) {
	tests := []struct {
		name string
		mode attachMode
	}{
		{name: "shared attach (collaborate)", mode: attachShared},
		{name: "readonly attach (watch)", mode: attachReadonly},
	}
	want := []string{"tmux", "attach", "-r", "-t", "honey_guest_argv_test"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFakeGuestSessionAlive(t, true)
			cmd, muxName, useZellij, err := ptyMuxTmuxCommand("honey_guest_argv_test", nil, tc.mode)
			require.NoError(t, err)
			require.Equal(t, "honey_guest_argv_test", muxName)
			require.False(t, useZellij)
			require.Equal(t, want, cmd.Args)
			require.NotContains(t, cmd.Args, "-d", "a guest client must never be able to detach the operator")
		})
	}
}

// TestPtyMuxTmuxCommand_GuestAttachDeadSessionErrors proves a guest can never
// create or respawn a session: when the session has ended (faked dead),
// shared/readonly attach must error instead of falling back to any
// new-session argv, for both mux name families.
func TestPtyMuxTmuxCommand_GuestAttachDeadSessionErrors(t *testing.T) {
	tests := []struct {
		name    string
		muxName string
		mode    attachMode
	}{
		{name: "shared on dead honey_ session", muxName: "honey_dead_session_test", mode: attachShared},
		{name: "readonly on dead honey_ session", muxName: "honey_dead_session_test", mode: attachReadonly},
		{name: "shared on dead honey-int- session", muxName: "honey-int-deaddead1234", mode: attachShared},
		{name: "readonly on dead honey-int- session", muxName: "honey-int-deaddead1234", mode: attachReadonly},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFakeGuestSessionAlive(t, false)
			cmd, _, _, err := ptyMuxTmuxCommand(tc.muxName, nil, tc.mode)
			require.Error(t, err)
			require.Nil(t, cmd)
			require.Contains(t, err.Error(), "has ended")
		})
	}
}

// TestPtyMuxTmuxCommand_GuestAttachRejectsInvalidName proves the mux name is
// re-validated immediately before it reaches a tmux argv on the guest path —
// independent of whatever validated it at grant-create time — keeping the
// "#nosec G204 -- muxName sanitized" invariant true for this call path too. A
// name that matches neither mux family must error before even consulting
// tmuxGuestSessionAlive (faked alive here, so a false result would mean the
// name check was skipped).
func TestPtyMuxTmuxCommand_GuestAttachRejectsInvalidName(t *testing.T) {
	withFakeGuestSessionAlive(t, true)
	cmd, _, _, err := ptyMuxTmuxCommand("rm -rf /; honey_evil", nil, attachShared)
	require.Error(t, err)
	require.Nil(t, cmd)
}

// TestGuestAttachTeardown_NoKillSession proves the load-bearing guest-teardown
// invariant end to end against a REAL tmux session: a guest's close_tab reaps
// only the guest's own attach client, never the operator's session — unlike
// the pre-task behavior where close_tab always ran kill-session. Skips
// cleanly when tmux is not on PATH (same pattern as
// TestInterceptResumeTmuxLifecycle; the release image always ships tmux).
func TestGuestAttachTeardown_NoKillSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; guest-attach teardown requires it")
	}

	name := fmt.Sprintf("honey_guest_teardown_%d", time.Now().UnixNano())
	require.True(t, validHoneyMuxSessionName(name))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "--", "cat").Run())

	cmd, _, _, err := ptyMuxTmuxCommand(name, nil, attachShared)
	require.NoError(t, err)
	ptmx, err := pty.Start(cmd)
	require.NoError(t, err)

	closeTabKill := make(chan struct{}, 1)
	ptyExited := make(chan struct{}) // never closes: this exercises close_tab, not a natural pty exit
	closeTabKill <- struct{}{}

	guestPID := cmd.Process.Pid

	// This is exactly how handleLiveTerminalAttach wires guest teardown: a
	// literal no-op killSession, relying on the plain reap of the guest's own
	// client — never a real tmux kill-session — and guestPath=true.
	ptyProxyTeardown(ptmx, cmd, name, false, closeTabKill, ptyExited, func() {}, true)

	// reapPtyProxyCmd calls (*os.Process).Kill+Wait, not (*exec.Cmd).Wait, so
	// cmd.ProcessState is never populated here — check liveness directly via a
	// signal-0 probe instead.
	require.Error(t, syscall.Kill(guestPID, 0), "the guest's own tmux attach client must be reaped (still running)")

	// The load-bearing proof: the OPERATOR's session is still alive. If a real
	// kill-session had run (the bug this task fixes), has-session would fail.
	require.NoError(t, exec.Command("tmux", "has-session", "-t", name).Run(), "operator's session must survive a guest close_tab")
}

// TestPtyProxyTeardown_PtyExitedGuestPathNeverKillsSession is the LOW-6
// regression: unlike the close_tab (×) branch (already covered above), the
// ptyExited branch used to call ptyMuxKillSessionIfExited unconditionally —
// so a GUEST whose own tmux client happened to exit naturally could still
// reap the operator's session, once all its panes were dead. guestPath must
// guard this branch too, so "a guest never kill-sessions" holds absolutely.
// The session here is deliberately left with a fully-exited pane (all panes
// dead) — exactly the condition ptyMuxKillSessionIfExited acts on — so this
// proves the guard, not a session that merely survived because the cleanup
// condition was never met.
func TestPtyProxyTeardown_PtyExitedGuestPathNeverKillsSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; guest-attach teardown requires it")
	}

	name := fmt.Sprintf("honey_guest_ptyexited_%d", time.Now().UnixNano())
	require.True(t, validHoneyMuxSessionName(name))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	// remain-on-exit keeps the session around (dead pane, no live process)
	// instead of tmux's default of destroying the session the instant its one
	// pane's command exits — set it BEFORE the short-lived command below
	// finishes, so tmuxSessionFullyExited has something to observe.
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "--", "sh", "-c", "sleep 0.3").Run())
	require.NoError(t, exec.Command("tmux", "set-option", "-t", name, "remain-on-exit", "on").Run())
	require.Eventually(t, func() bool { return tmuxSessionFullyExited(name) }, 3*time.Second, 50*time.Millisecond, "the session's one pane must finish exiting")

	// A throwaway pty/cmd stands in for the guest's own attach client being
	// torn down; only muxName/guestPath drive the behavior under test.
	cmd := exec.Command("cat")
	ptmx, err := pty.Start(cmd)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	ptyExited := make(chan struct{})
	close(ptyExited) // simulate the guest's own client having exited naturally
	closeTabKill := make(chan struct{}, 1)

	ptyProxyTeardown(ptmx, cmd, name, false, closeTabKill, ptyExited, func() {}, true)

	require.NoError(t, exec.Command("tmux", "has-session", "-t", name).Run(),
		"a guest's natural ptyExited teardown must never kill the operator's session, fully-exited panes or not")
}

// newFakePtyPair returns two connected, bidirectional *os.File ends (an
// AF_UNIX socketpair) standing in for a pty master/slave: ptmx is what
// ptyProxyRunBridge treats as the multiplexer pty, and peer is the test's
// hand on "whatever is behind it" — read peer to see what the bridge wrote
// (stdin), write peer to make the bridge see stdout. Both fds are put in
// non-blocking mode before wrapping: per os.NewFile's doc, that is what makes
// Go return a POLLABLE File (SetDeadline/Close-unblocks-a-pending-Read all
// work) instead of falling back to a raw blocking syscall — which real ptys
// (github.com/creack/pty on darwin in particular) do NOT get by default, and
// which is not what this test is trying to exercise anyway.
func newFakePtyPair(t *testing.T) (ptmx, peer *os.File) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	require.NoError(t, syscall.SetNonblock(fds[0], true))
	require.NoError(t, syscall.SetNonblock(fds[1], true))
	return os.NewFile(uintptr(fds[0]), "fake-ptmx"), os.NewFile(uintptr(fds[1]), "fake-peer")
}

// TestPtyProxyRunBridge_ReadOnlySkipsGuestStdin proves ptyProxyStdinPolicy's
// DropStdin flag — the watch-grant belt-and-braces guard alongside tmux's own
// `-r` attach — never writes a guest's stdin frame into the pty, and that the
// zero-value policy (every pre-existing caller) is unaffected.
func TestPtyProxyRunBridge_ReadOnlySkipsGuestStdin(t *testing.T) {
	t.Run("DropStdin=true drops guest stdin", func(t *testing.T) {
		testPtyProxyRunBridgeStdinGate(t, ptyProxyStdinPolicy{DropStdin: true})
	})
	t.Run("zero value forwards stdin (unchanged)", func(t *testing.T) {
		testPtyProxyRunBridgeStdinGate(t, ptyProxyStdinPolicy{})
	})
}

func testPtyProxyRunBridgeStdinGate(t *testing.T, stdin ptyProxyStdinPolicy) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_stdin_gate_test", closeTabKill, stdin)
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("hello-guest")))

	_ = peer.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	buf := make([]byte, 64)
	n, err := peer.Read(buf)
	if stdin.DropStdin {
		require.Error(t, err, "a watch guest's stdin must never reach the shared session")
		var netErr net.Error
		require.ErrorAs(t, err, &netErr)
		require.True(t, netErr.Timeout(), "expected a read timeout (no data ever written), got: %v", err)
	} else {
		require.NoError(t, err)
		require.Equal(t, "hello-guest", string(buf[:n]))
	}

	// Close both ends before the deferred goleak check: this is a plain
	// socket, so Close reliably unblocks the bridge's pending ptmx.Read (and
	// closing conn unblocks its pending conn.ReadMessage) on every platform,
	// letting the bridge's two goroutines exit and ptyProxyRunBridge return.
	_ = ptmx.Close()
	_ = conn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}

// TestPtyProxyRunBridge_DisconnectUnblocksIdlePtmxRead is the LOW-7
// regression: a plain browser disconnect (the conn erroring — never ptmx)
// must promptly unblock an idle-blocked ptmx.Read (no pty output pending),
// instead of leaving a guest's own tmux client attached to the OPERATOR's
// session until some unrelated later byte happens to arrive. Only the
// client's conn is closed here — ptmx/peer are deliberately never touched or
// written to — so a hang means the fix regressed (pre-fix, this exact
// scenario could block forever on an idle shared session).
func TestPtyProxyRunBridge_DisconnectUnblocksIdlePtmxRead(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()
	defer ptmx.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		// Discard the returned channel deliberately: it only ever closes on a
		// genuine natural pty exit (never on this disconnect-triggered
		// cancellation), same as production callers of ptyProxyRunBridge — what
		// this test checks is that the CALL ITSELF returns promptly.
		ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_disconnect_test", closeTabKill, ptyProxyStdinPolicy{})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	// Simulate a plain browser disconnect: drop the client side without
	// either end ever sending a byte, so the ptmx reader has nothing to wake
	// it up naturally.
	require.NoError(t, conn.Close())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after a plain disconnect (idle-blocked ptmx.Read never unblocked)")
	}
}

// TestPtyProxyRunBridge_DeadBothDirectionsStillTearsDown is the NEW-12
// residual regression: LOW-7's ptmx watcher only covers the READ direction.
// A guest's connection can be dead in BOTH directions at once (network
// partition, frozen tab — exactly what NEW-12 named, where TCP keepalive
// doesn't help), with the operator's pane still producing output: the
// ptmx-reading goroutine's wsOut.Write eventually blocks and times out
// (wsWriteTimeout), calling bridgeCancel — but the conn-reading goroutine's
// bare conn.ReadMessage() has no deadline and no select on
// bridgeCtx.Done(), so without a symmetric watcher it stayed blocked
// forever: wg.Wait() never returned, ptyProxyTeardown never ran, and the
// guest's tmux client stayed attached to the operator's session forever.
// This drives a real, continuously-producing ptmx against a real conn that
// never reads or writes again, and asserts the bridge call itself still
// returns.
func TestPtyProxyRunBridge_DeadBothDirectionsStillTearsDown(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	origTimeout := wsWriteTimeout
	wsWriteTimeout = 100 * time.Millisecond
	t.Cleanup(func() { wsWriteTimeout = origTimeout })

	ptmx, peer := newFakePtyPair(t)
	defer ptmx.Close()
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_deadconn_test", closeTabKill, ptyProxyStdinPolicy{})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()
	// Deliberately dead in BOTH directions: never call conn.ReadMessage()
	// (frozen tab) and never send anything either (network partition) — the
	// only thing moving is the simulated operator pane below, backing the
	// connection up until the write side's deadline fires.

	go func() {
		chunk := bytes.Repeat([]byte{'x'}, 1<<20) // 1 MiB per write
		for i := 0; i < 64; i++ {                 // up to 64 MiB — comfortably overflows any realistic buffer
			if _, err := peer.Write(chunk); err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return with a connection dead in both directions")
	}
}

// TestPtyProxyHandleCtrl_IgnoreResize is the LOW-5 regression: a guest's
// resize control frame is dropped entirely when ignoreResize is set, leaving
// the operator's pane size untouched — the operator alone drives sizing. An
// operator/non-guest bridge (ignoreResize=false) keeps resizing exactly as
// before.
func TestPtyProxyHandleCtrl_IgnoreResize(t *testing.T) {
	cmd := exec.Command("cat")
	ptmx, err := pty.Start(cmd)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	require.NoError(t, pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24}))

	payload := []byte(`{"type":"resize","cols":200,"rows":60}`)
	closeTabKill := make(chan struct{}, 1)

	stop := ptyProxyHandleCtrl(ptmx, nil, "honey_resize_test", closeTabKill, payload, true)
	require.False(t, stop)
	ws, err := pty.GetsizeFull(ptmx)
	require.NoError(t, err)
	require.EqualValues(t, 80, ws.Cols, "a guest resize frame must never reach pty.Setsize")
	require.EqualValues(t, 24, ws.Rows, "a guest resize frame must never reach pty.Setsize")

	stop = ptyProxyHandleCtrl(ptmx, nil, "honey_resize_test", closeTabKill, payload, false)
	require.False(t, stop)
	ws, err = pty.GetsizeFull(ptmx)
	require.NoError(t, err)
	require.EqualValues(t, 200, ws.Cols, "an operator/non-guest resize frame must still resize (unchanged)")
	require.EqualValues(t, 60, ws.Rows, "an operator/non-guest resize frame must still resize (unchanged)")
}

// TestPtyProxyRunBridge_IgnoreResizeSkipsInitialHelloSize is the LOW-5
// round-2 residual: resize control FRAMES were already dropped for guests
// (above), but the guest's initial hello cols/rows still reached
// pty.Setsize at bridge start (measured: a 40x10 guest shrank a detached
// operator's 200x50 window to 40x9, and it stayed). IgnoreResize must now
// skip that initial Setsize too, for both guest modes; the zero-value
// operator/non-guest path is unaffected.
func TestPtyProxyRunBridge_IgnoreResizeSkipsInitialHelloSize(t *testing.T) {
	// A real pty (not the fake AF_UNIX pair used elsewhere) is needed to call
	// pty.GetsizeFull — but a real, empty `cat` pty's Read blocks in a way
	// SetReadDeadline cannot interrupt on darwin (see newFakePtyPair's own
	// comment), so this test does not wait for the bridge to fully tear
	// down — only for the SYNCHRONOUS size setup at the very top of
	// ptyProxyRunBridge to have run, which is all this fix touches. t.Cleanup
	// killing the process/closing ptmx is what eventually lets the bridge's
	// goroutines unwind.
	cmd := exec.Command("cat")
	ptmx, err := pty.Start(cmd)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	require.NoError(t, pty.Setsize(ptmx, &pty.Winsize{Cols: 80, Rows: 24}))

	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, uerr := upgrader.Upgrade(w, r, nil)
		require.NoError(t, uerr)
		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 40, Rows: 10}
		go ptyProxyRunBridge(ptmx, c, (*engine.SessionRecorder)(nil), hello, "honey_ignoreresize_hello_test", closeTabKill,
			ptyProxyStdinPolicy{IgnoreResize: true, DropStdin: true})
	}))
	t.Cleanup(ts.Close)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	wsConn, _, dialErr := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, dialErr)
	t.Cleanup(func() { _ = wsConn.Close() })

	// Give the bridge a moment to run its (skipped) initial Setsize, then
	// assert the pty's size is still what it was BEFORE the guest ever
	// attached — never the guest's 40x10 hello.
	time.Sleep(100 * time.Millisecond)
	ws, gerr := pty.GetsizeFull(ptmx)
	require.NoError(t, gerr)
	require.EqualValues(t, 80, ws.Cols, "a guest's hello size must never reach the initial pty.Setsize")
	require.EqualValues(t, 24, ws.Rows, "a guest's hello size must never reach the initial pty.Setsize")
}

// swapTmuxRunGuest installs a fake tmuxRunGuest (tmuxSendKeysHex's
// relay-local, timeout-bounded exec seam — distinct from the shared tmuxRun)
// and returns a restore func. Tests using it must not run in parallel, same
// caveat as swapTmuxRun.
func swapTmuxRunGuest(fn func(...string) ([]byte, error)) func() {
	orig := tmuxRunGuest
	tmuxRunGuest = fn
	return func() { tmuxRunGuest = orig }
}

// TestTmuxSendKeysHex_ArgvNeverContainsRawBytes is the HIGH-1 unit-level
// proof, via a fake tmux runner: tmuxSendKeysHex builds a send-keys -H argv
// out of self-generated two-digit hex per byte, and the guest's raw bytes
// never appear verbatim as an argument. "\x02c" is tmux's own C-b c prefix
// sequence — the exact RCE trigger this task closes — fed through this
// function it must produce nothing but "02 63", never anything a tmux client
// would itself interpret as a keybinding.
func TestTmuxSendKeysHex_ArgvNeverContainsRawBytes(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    []string
	}{
		{
			name:    "prefix-c byte sequence (the RCE trigger)",
			payload: []byte("\x02c"),
			want:    []string{"send-keys", "-H", "-t", "honey_target:", "02", "63"},
		},
		{
			name:    "printable text",
			payload: []byte("ls\r"),
			want:    []string{"send-keys", "-H", "-t", "honey_target:", "6c", "73", "0d"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotArgs []string
			restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
				gotArgs = args
				return nil, nil
			})
			defer restore()

			require.NoError(t, tmuxSendKeysHex("honey_target:", tc.payload))
			require.Equal(t, tc.want, gotArgs)
			for _, arg := range gotArgs {
				require.NotEqual(t, string(tc.payload), arg, "raw guest bytes must never appear verbatim as an argv element")
			}
		})
	}
}

// TestTmuxSendKeysHex_RejectsOversizedFrameWithoutExec is the NEW-5
// regression: a payload over maxRelayFrameBytes is refused OUTRIGHT — the
// fake tmuxRunGuest here fails the test if it is ever called at all, so
// this also proves the round-2 "all-or-nothing per frame" judgment: no
// partial relay is even attempted for an oversized frame.
func TestTmuxSendKeysHex_RejectsOversizedFrameWithoutExec(t *testing.T) {
	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		t.Fatalf("tmux must never be exec'd for an oversized frame, got args %v", args)
		return nil, nil
	})
	defer restore()

	payload := bytes.Repeat([]byte{0x41}, maxRelayFrameBytes+1)
	err := tmuxSendKeysHex("honey_target:", payload)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds")
}

// TestTmuxSendKeysHex_PropagatesRunError proves a tmux failure surfaces as a
// wrapped error instead of being silently swallowed.
func TestTmuxSendKeysHex_PropagatesRunError(t *testing.T) {
	restore := swapTmuxRunGuest(func(...string) ([]byte, error) {
		return []byte("can't find pane"), errors.New("exit status 1")
	})
	defer restore()

	err := tmuxSendKeysHex("honey_target:", []byte("x"))
	require.Error(t, err)
}

// TestTmuxSendKeysHex_TimesOutOnRealHang is the NEW-1 regression against a
// REAL (fake) tmux binary that hangs — proving the actual
// exec.CommandContext + tmuxSendKeysRunTimeout mechanism works, not just
// that a faked error propagates. A tiny shell script named "tmux" is put
// first on PATH and sleeps far longer than a shrunk timeout; the exec must
// still return within a small bounded wall-clock window (well under the
// script's sleep), proving the child was actually killed on deadline, not
// merely that the call eventually returned on its own.
func TestTmuxSendKeysHex_TimesOutOnRealHang(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep 5\n"
	fakeTmux := dir + "/tmux"
	require.NoError(t, os.WriteFile(fakeTmux, []byte(script), 0o755)) // #nosec G306 -- test fixture, must be executable

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	origTimeout := tmuxSendKeysRunTimeout
	tmuxSendKeysRunTimeout = 100 * time.Millisecond
	t.Cleanup(func() { tmuxSendKeysRunTimeout = origTimeout })

	start := time.Now()
	err := tmuxSendKeysHex("honey_target:", []byte("g"))
	elapsed := time.Since(start)

	require.Error(t, err, "a hung tmux must surface as an error, never block forever")
	require.Less(t, elapsed, 3*time.Second, "the call must return near the shrunk timeout, not wait out the fake tmux's 5s sleep")
}

// TestPtyProxyRunBridge_CollaborateRelaysViaSendKeys proves the bridge wiring
// for a collaborate guest (RelayTarget set): inbound stdin bytes are relayed
// via tmuxSendKeysHex — a fake tmux runner here, asserting the exact argv —
// and never written to the connection's own ptmx. \x02c (tmux's C-b c prefix
// sequence, the RCE trigger HIGH-1 closes) produces nothing but a send-keys
// argv, never a byte the guest's own (read-only) tmux client could itself
// interpret.
func TestPtyProxyRunBridge_CollaborateRelaysViaSendKeys(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	var gotArgs []string
	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	})
	defer restore()

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_relay_test", closeTabKill,
			ptyProxyStdinPolicy{RelayTarget: "honey_relay_test:"})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("\x02c")))

	require.Eventually(t, func() bool { return gotArgs != nil }, 2*time.Second, 10*time.Millisecond, "expected a send-keys relay call")
	require.Equal(t, []string{"send-keys", "-H", "-t", "honey_relay_test:", "02", "63"}, gotArgs)

	// The load-bearing negative: the byte must never reach ptmx directly (the
	// pre-fix, vulnerable path) — the peer end has nothing to read.
	_ = peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 8)
	_, rerr := peer.Read(buf)
	require.Error(t, rerr, "a collaborate guest's raw bytes must never be written straight to ptmx")

	_ = ptmx.Close()
	_ = conn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}

// TestFilterTerminalReports is the NEW-2 regression: a real terminal (and the
// guest's own xterm.js) answers Device Attributes / Cursor Position / OSC
// color queries automatically, and — because every guest byte is relayed
// into the pane — an unfiltered reply would land as literal text at the
// operator's shell prompt or duplicate a genuine reply an app is waiting on.
// Table-drives the reply shapes named in the brief (CSI .../c, /R, /n,
// OSC/DCS) as dropped, and proves ordinary typing (letters, control chars,
// arrow/function-key CSI sequences) passes through byte-for-byte.
func TestFilterTerminalReports(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
		want    []byte
	}{
		{name: "DA1 reply dropped", payload: []byte("\x1b[?1;2c"), want: []byte{}},
		{name: "DA2 reply dropped", payload: []byte("\x1b[>1;95;0c"), want: []byte{}},
		{name: "CPR reply dropped", payload: []byte("\x1b[24;80R"), want: []byte{}},
		{name: "device status report dropped", payload: []byte("\x1b[0n"), want: []byte{}},
		{name: "OSC color reply dropped (BEL-terminated)", payload: []byte("\x1b]11;rgb:0000/0000/0000\x07"), want: []byte{}},
		{name: "OSC color reply dropped (ST-terminated)", payload: []byte("\x1b]10;rgb:ffff/ffff/ffff\x1b\\"), want: []byte{}},
		{name: "DCS reply dropped", payload: []byte("\x1bP1$r0\x1b\\"), want: []byte{}},
		{
			name:    "a report reply mixed into ordinary output is stripped, typing survives",
			payload: []byte("ls\x1b[?1;2c\r"), want: []byte("ls\r"),
		},
		{name: "ordinary typing untouched", payload: []byte("hello world\r"), want: []byte("hello world\r")},
		{name: "arrow key CSI untouched (ends in A, not a report final byte)", payload: []byte("\x1b[A"), want: []byte("\x1b[A")},
		{name: "Home key CSI untouched (ends in ~)", payload: []byte("\x1b[1~"), want: []byte("\x1b[1~")},
		{name: "the HIGH-1 prefix byte (not an ESC sequence at all) untouched", payload: []byte("\x02c"), want: []byte("\x02c")},
		{name: "empty payload", payload: []byte{}, want: []byte{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f terminalReportFilter
			got := f.filter(tc.payload)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestTerminalReportFilter_StraddlesFrames is the NEW-14 regression: round
// 2's filter was stateless per WS frame, so a reply (or a trailing
// incomplete escape sequence) split across two frames leaked through
// untouched. One filter instance fed both frames in sequence must strip the
// reply completely, and must still pass ordinary typing that follows once
// the held sequence is resolved.
func TestTerminalReportFilter_StraddlesFrames(t *testing.T) {
	t.Run("CPR reply split mid-CSI", func(t *testing.T) {
		var f terminalReportFilter
		require.Equal(t, []byte{}, f.filter([]byte("\x1b[24")))
		require.Equal(t, []byte{}, f.filter([]byte(";80R")))
	})

	t.Run("OSC color reply split before its terminator", func(t *testing.T) {
		var f terminalReportFilter
		require.Equal(t, []byte{}, f.filter([]byte("\x1b]11;rgb:0000/0000")))
		require.Equal(t, []byte{}, f.filter([]byte("/0000\x07")))
	})

	t.Run("a lone trailing ESC is held then resolves into a dropped report", func(t *testing.T) {
		var f terminalReportFilter
		require.Equal(t, []byte{}, f.filter([]byte("\x1b")))
		require.Equal(t, []byte{}, f.filter([]byte("[6n")))
	})

	t.Run("held sequence does not swallow ordinary typing that follows in the SAME next frame", func(t *testing.T) {
		var f terminalReportFilter
		require.Equal(t, []byte{}, f.filter([]byte("\x1b[24")))
		require.Equal(t, []byte("hi\r"), f.filter([]byte(";80Rhi\r")))
	})

	t.Run("a held prefix that never resolves is eventually flushed, not held forever", func(t *testing.T) {
		var f terminalReportFilter
		require.Equal(t, []byte{}, f.filter([]byte("\x1b[")))
		// A very long parameter string with no final byte anywhere: once the
		// combined pending+new bytes exceed maxPendingReportBytes, this is no
		// longer treated as "possibly a report" and is flushed as literal
		// data instead of buffered forever.
		garbage := bytes.Repeat([]byte("9"), maxPendingReportBytes+16)
		got := f.filter(garbage)
		require.NotEmpty(t, got, "an unbounded non-terminating prefix must eventually flush, not buffer forever")
	})

	t.Run("independent filter instances never share state", func(t *testing.T) {
		var f1, f2 terminalReportFilter
		require.Equal(t, []byte{}, f1.filter([]byte("\x1b[24")))
		// f2 never saw the first half, so this looks like ordinary (odd)
		// input to it, not a completion of anything.
		got := f2.filter([]byte(";80R"))
		require.Equal(t, []byte(";80R"), got)
	})
}

// TestPtyProxyRunBridge_CollaborateFiltersTerminalReports proves the filter
// is actually wired into the relay path: a DA1 reply byte string is dropped
// before it ever reaches tmuxSendKeysHex (no exec at all — the fake here
// fails the test if called), while ordinary typing sent in the SAME
// connection still reaches it normally.
func TestPtyProxyRunBridge_CollaborateFiltersTerminalReports(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	var gotArgs []string
	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	})
	defer restore()

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_filter_test", closeTabKill,
			ptyProxyStdinPolicy{RelayTarget: "honey_filter_test:"})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	// A pure DA1 reply: fully filtered, so nothing should ever reach tmux.
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("\x1b[?1;2c")))
	time.Sleep(100 * time.Millisecond)
	require.Nil(t, gotArgs, "a pure terminal-report reply must never reach tmuxSendKeysHex")

	// Ordinary typing in the same connection still relays normally.
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("hi\r")))
	require.Eventually(t, func() bool { return gotArgs != nil }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, []string{"send-keys", "-H", "-t", "honey_filter_test:", "68", "69", "0d"}, gotArgs)

	_ = ptmx.Close()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}

// TestPtyProxyRunBridge_RelayFailureRecordsDropAndNotifiesGuest is the NEW-6
// regression: a failed relay must record that the bytes were DROPPED (never
// a false "stdin" success claiming the pane received something it never
// did), and — per the round-2 judgment that a guest typing into a void will
// blindly retype, possibly a destructive command — must tell the guest over
// the socket.
func TestPtyProxyRunBridge_RelayFailureRecordsDropAndNotifiesGuest(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	restore := swapTmuxRunGuest(func(...string) ([]byte, error) {
		return nil, errors.New("simulated relay failure")
	})
	defer restore()

	rec, err := engine.NewSessionRecorder(engine.SessionRecorderOptions{
		Dir: t.TempDir(), Trigger: "test", Mode: "ssh", HostName: "guest-relay-fail",
	})
	require.NoError(t, err)

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		ptyProxyRunBridge(ptmx, conn, rec, hello, "honey_dropnotify_test", closeTabKill,
			ptyProxyStdinPolicy{RelayTarget: "honey_dropnotify_test:"})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("rm -rf /\r")))

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, raw, rerr := conn.ReadMessage()
	require.NoError(t, rerr)
	require.Equal(t, websocket.TextMessage, mt)
	require.Contains(t, string(raw), "dropped", "the guest must be told its input never landed")

	// NEW-17: the drop notice must use a distinct "notice" field, never
	// "error" — the client renders "error" into the terminal buffer and
	// latches it as a fatal condition, neither of which is right for a
	// merely-transient delivery failure.
	var parsed struct {
		Notice string `json:"notice"`
		Error  string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &parsed))
	require.NotEmpty(t, parsed.Notice, "drop notice must be carried in the \"notice\" field")
	require.Empty(t, parsed.Error, "drop notice must NOT use the \"error\" field")

	_ = ptmx.Close()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
	require.NoError(t, rec.Close())

	recorded, err := os.ReadFile(rec.Path())
	require.NoError(t, err)
	var sawDropError, sawFalseStdinSuccess bool
	for _, line := range bytes.Split(bytes.TrimSpace(recorded), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var evt struct {
			Type      string `json:"type"`
			Direction string `json:"direction"`
			Message   string `json:"message"`
		}
		require.NoError(t, json.Unmarshal(line, &evt))
		if evt.Type == "error" && strings.Contains(evt.Message, "dropped") {
			sawDropError = true
		}
		if evt.Type == "data" && evt.Direction == "stdin" {
			sawFalseStdinSuccess = true
		}
	}
	require.True(t, sawDropError, "expected a recorded error event describing the dropped bytes")
	require.False(t, sawFalseStdinSuccess, "must never record a stdin success for bytes the pane never received")
}

// TestHandleLiveTerminalAttach_UnsupportedModeFailsClosed is the NEW-7
// regression: any attachMode other than the two known guest modes must be
// rejected before starting any process — never fall through to the
// stdin-policy switch's zero value, which (round 1) forwarded raw guest
// bytes straight to ptmx, restoring the HIGH-1 hole.
func TestHandleLiveTerminalAttach_UnsupportedModeFailsClosed(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		err = handleLiveTerminalAttach(conn, "honey_unsupported_mode_test", attachExclusive, 80, 24, nil, termGuardInputs{})
		require.Error(t, err, "attachExclusive (or any mode besides shared/readonly) must be rejected here")
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	_ = conn.Close()
}

// TestHandleLiveTerminalAttach_NeverPinsWindowSize is the NEW-10 regression:
// round 2's guest attach used to set tmux's window-size option to "manual"
// on the OPERATOR's session and never restore it, permanently breaking the
// operator's own browser resize for the life of that session. A guest
// attach must leave window-size at whatever it already was (here, the tmux
// default "latest") — never touch it at all. Skips cleanly when tmux is not
// on PATH.
func TestHandleLiveTerminalAttach_NeverPinsWindowSize(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	name := fmt.Sprintf("honey_winsize_nopin_%d", time.Now().UnixNano())
	require.True(t, validHoneyMuxSessionName(name))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-x", "200", "-y", "50", "-s", name, "--", "cat").Run())

	before, err := exec.Command("tmux", "show-options", "-t", name, "window-size").Output()
	require.NoError(t, err)

	cmd, _, _, err := ptyMuxTmuxCommand(name, nil, attachReadonly)
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	after, err := exec.Command("tmux", "show-options", "-t", name, "window-size").Output()
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "a guest attach must never change window-size")
	require.NotContains(t, string(after), "manual", "a guest attach must never pin window-size to manual")
}

// TestPtyProxyRunBridge_CollaborateGuardBlocksDeniedCommand is the task-P
// regression: the per-command guard sits on the relay's byte stream AFTER
// the report filter and BEFORE tmuxSendKeysHex (the HIGH-1 mediation seam),
// gating a collaborate guest exactly like the SSH gateway gates an operator.
// A nil Enforcer/Guardrails still denies via cmdgate's unconditional
// critical-risk floor — a collaborate guest is always guarded (enforce),
// never dependent on OPA being configured. The denied line's trailing Enter
// must never reach tmux verbatim; the guard replaces it with a Ctrl-U so the
// pending line is discarded on the target instead of executing.
func TestPtyProxyRunBridge_CollaborateGuardBlocksDeniedCommand(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	var gotArgs []string
	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	})
	defer restore()

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		guard := termGuardInputs{Actor: "share:test", Record: hosts.Record{Name: "target1"}, Mode: termguard.ModeEnforce}
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_guard_deny_test", closeTabKill,
			ptyProxyStdinPolicy{RelayTarget: "honey_guard_deny_test:", GuestGuard: &guard})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("rm -rf /\r")))

	require.Eventually(t, func() bool { return gotArgs != nil }, 2*time.Second, 10*time.Millisecond, "expected a send-keys relay call")
	require.NotContains(t, gotArgs, "0d", "a denied command's Enter (0x0d) must never reach tmux")
	require.Contains(t, gotArgs, "15", "a denied command's Enter must be replaced with Ctrl-U (0x15)")

	_ = ptmx.Close()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}

// TestPtyProxyRunBridge_CollaborateGuardAllowsBenignCommand is the allow-path
// counterpart: a benign command line relays through the guard byte-for-byte
// unchanged, proving the guard only mutates what it actually denies.
func TestPtyProxyRunBridge_CollaborateGuardAllowsBenignCommand(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	var gotArgs []string
	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		gotArgs = args
		return nil, nil
	})
	defer restore()

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		guard := termGuardInputs{Actor: "share:test", Record: hosts.Record{Name: "target1"}, Mode: termguard.ModeEnforce}
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_guard_allow_test", closeTabKill,
			ptyProxyStdinPolicy{RelayTarget: "honey_guard_allow_test:", GuestGuard: &guard})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("ls\r")))

	require.Eventually(t, func() bool { return gotArgs != nil }, 2*time.Second, 10*time.Millisecond, "expected a send-keys relay call")
	require.Equal(t, []string{"send-keys", "-H", "-t", "honey_guard_allow_test:", "6c", "73", "0d"}, gotArgs,
		"an allowed command must relay byte-for-byte unchanged")

	_ = ptmx.Close()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}

// TestPtyProxyRunBridge_WatchGuestNeverConsultsGuard proves a watch guest has
// no guard/stdin path at all: DropStdin's continue happens before the relay
// branch (and thus before the guard) is ever reached, so even a caller
// mistake that sets GuestGuard alongside DropStdin can never cause a tmux
// exec — asserted here by failing the test outright if tmuxRunGuest is ever
// invoked, and by the pre-existing HIGH-1 assertion that nothing reaches
// ptmx either.
func TestPtyProxyRunBridge_WatchGuestNeverConsultsGuard(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	restore := swapTmuxRunGuest(func(args ...string) ([]byte, error) {
		t.Errorf("tmux must never be exec'd for a watch guest, got args %v", args)
		return nil, nil
	})
	defer restore()

	ptmx, peer := newFakePtyPair(t)
	defer peer.Close()

	upgrader := websocket.Upgrader{}
	done := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()

		closeTabKill := make(chan struct{}, 1)
		hello := WSHello{Cols: 80, Rows: 24}
		// A GuestGuard set here (mirroring a careless future caller) must
		// still never be consulted for a watch guest: RelayTarget stays
		// empty, so the guard-build gate (stdin.RelayTarget != "") excludes
		// it structurally, on top of tmux's own read-only attach.
		guard := termGuardInputs{Mode: termguard.ModeEnforce}
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_watch_guard_test", closeTabKill,
			ptyProxyStdinPolicy{DropStdin: true, GuestGuard: &guard})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("rm -rf /\r")))

	_ = peer.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 8)
	_, rerr := peer.Read(buf)
	require.Error(t, rerr, "a watch guest's bytes must never reach ptmx")

	_ = ptmx.Close()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}
