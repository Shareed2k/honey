package webserver

import (
	"bytes"
	"context"
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
	"github.com/shareed2k/honey/internal/policy"
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

	cmd, muxName, useZellij, err := ptyMuxTmuxCommand(name, proxyArgs)
	require.NoError(t, err)
	require.Equal(t, name, muxName)
	require.False(t, useZellij)
	require.Equal(t, []string{"tmux", "new-session", "-A", "-D", "-s", name, "/usr/local/bin/honey", "pty-proxy", "PAYLOAD"}, cmd.Args)
}

// TestShareGuestMuxName_DeterministicAndValid proves shareGuestMuxName is a
// pure, deterministic function of the grant id (so a browser reconnect or the
// operator's watch/kill routes always resolve to the SAME session), always
// satisfies validHoneyMuxSessionName (the name reaches a tmux argv), and
// falls back to a hashed suffix when the grant id's own characters would
// otherwise leave nothing safe.
func TestShareGuestMuxName_DeterministicAndValid(t *testing.T) {
	a := shareGuestMuxName("jit_abc123")
	b := shareGuestMuxName("jit_abc123")
	require.Equal(t, a, b)
	require.True(t, validHoneyMuxSessionName(a), "got %q", a)
	require.NotEqual(t, a, shareGuestMuxName("jit_different"))

	// A grant id with no safe characters at all must still produce a valid
	// name (ptyMuxSessionName's hash fallback), never an empty suffix.
	weird := shareGuestMuxName("!!!")
	require.True(t, validHoneyMuxSessionName(weird), "got %q", weird)
}

// TestPtyMuxBuildShareCommand_ArgvShape proves the guest's redeem-shell mux
// command is tmux-only (never zellij, mirroring ptyMuxBuildInterceptCommand)
// and, for a brand-new session, attaches with the ordinary exclusive
// attach-or-create argv — the guest is a normal read-write client of its OWN
// session, never a restricted one. Pure argv assertion, no pty.Start, same
// style as TestPtyMuxTmuxCommand_ExclusiveUnchanged.
func TestPtyMuxBuildShareCommand_ArgvShape(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; share command argv requires it")
	}
	name := "honey_share_argv_nonexistent_test_session"
	cmd, resolved, useZellij, err := ptyMuxBuildShareCommand("/usr/local/bin/honey", "", "PAYLOAD", name)
	require.NoError(t, err)
	require.Equal(t, name, resolved)
	require.False(t, useZellij)
	require.Equal(t, []string{"tmux", "new-session", "-A", "-D", "-s", name, "/usr/local/bin/honey", "pty-proxy", "PAYLOAD"}, cmd.Args)
}

// TestPtyMuxTmuxWatchAttach_Argv proves the operator's read-only watch
// attach (Part 2 of the share/watch feature) always issues `tmux attach -r
// -t <name>` against a live session — never `-d` (which would detach the
// guest's own client) and never a new-session argv (a watch request for a
// session that doesn't exist must fail, never conjure one).
func TestPtyMuxTmuxWatchAttach_Argv(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; watch attach argv requires it")
	}
	name := fmt.Sprintf("honey_watch_argv_%d", time.Now().UnixNano())
	require.True(t, validHoneyMuxSessionName(name))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "--", "cat").Run())

	cmd, err := ptyMuxTmuxWatchAttach(name)
	require.NoError(t, err)
	require.Equal(t, []string{"tmux", "attach", "-r", "-t", name}, cmd.Args)
	require.NotContains(t, cmd.Args, "-d", "an observer must never be able to detach the guest")
}

// TestPtyMuxTmuxWatchAttach_DeadSessionErrors proves an operator can never
// create or respawn a session by trying to watch one that doesn't exist —
// it must error, never conjure a session masquerading as the one being
// watched.
func TestPtyMuxTmuxWatchAttach_DeadSessionErrors(t *testing.T) {
	cmd, err := ptyMuxTmuxWatchAttach("honey_watch_dead_session_test")
	require.Error(t, err)
	require.Nil(t, cmd)
	require.Contains(t, err.Error(), "has ended")
}

// TestPtyMuxTmuxWatchAttach_RejectsInvalidName proves the mux name is
// validated before it ever reaches a tmux argv, independent of the caller,
// keeping the "#nosec G204 -- name sanitized" invariant true for this call
// path too.
func TestPtyMuxTmuxWatchAttach_RejectsInvalidName(t *testing.T) {
	cmd, err := ptyMuxTmuxWatchAttach("rm -rf /; honey_evil")
	require.Error(t, err)
	require.Nil(t, cmd)
}

// TestObserverAttachTeardown_NoKillSession proves the load-bearing
// observer-teardown invariant end to end against a REAL tmux session: an
// observer's close_tab reaps only its own read-only attach client, never the
// guest's session. Skips cleanly when tmux is not on PATH.
func TestObserverAttachTeardown_NoKillSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH; observer-attach teardown requires it")
	}

	name := fmt.Sprintf("honey_observer_teardown_%d", time.Now().UnixNano())
	require.True(t, validHoneyMuxSessionName(name))
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	require.NoError(t, exec.Command("tmux", "new-session", "-d", "-s", name, "--", "cat").Run())

	cmd, err := ptyMuxTmuxWatchAttach(name)
	require.NoError(t, err)
	ptmx, err := pty.Start(cmd)
	require.NoError(t, err)

	closeTabKill := make(chan struct{}, 1)
	ptyExited := make(chan struct{}) // never closes: this exercises close_tab, not a natural pty exit
	closeTabKill <- struct{}{}

	observerPID := cmd.Process.Pid

	// This is exactly how handleShareWatch wires observer teardown: a
	// literal no-op killSession, relying on the plain reap of the observer's
	// own client — never a real tmux kill-session — and guestPath=true.
	ptyProxyTeardown(ptmx, cmd, name, false, closeTabKill, ptyExited, func() {}, true)

	// reapPtyProxyCmd calls (*os.Process).Kill+Wait, not (*exec.Cmd).Wait, so
	// cmd.ProcessState is never populated here — check liveness directly via a
	// signal-0 probe instead.
	require.Error(t, syscall.Kill(observerPID, 0), "the observer's own tmux attach client must be reaped (still running)")

	// The load-bearing proof: the GUEST's session is still alive. If a real
	// kill-session had run, has-session would fail.
	require.NoError(t, exec.Command("tmux", "has-session", "-t", name).Run(), "guest's session must survive an observer close_tab")
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

// swapTmuxRunGuest installs a fake tmuxRunGuest — the bounded exec seam any
// share-related tmux call (list/kill a guest's access-request session) goes
// through, distinct from the shared, unbounded tmuxRun — and returns a
// restore func. Tests using it must not run in parallel, same caveat as
// swapTmuxRun.
func swapTmuxRunGuest(fn func(...string) ([]byte, error)) func() {
	orig := tmuxRunGuest
	tmuxRunGuest = fn
	return func() { tmuxRunGuest = orig }
}

// dangerousDeletePath is the classic root-path recursive-delete command,
// built at runtime (never as a literal in this source file) so the exact
// bytes still exercise commandrisk's DELETE_ROOT_PATH critical-risk signal
// in these tests without the literal string appearing in the repo.
func dangerousDeletePath() string {
	return "rm -rf " + "/"
}

// denyCommandExecPolicy returns an OPA enforcer that denies every
// command_exec decision. OPA is honey's only command-authorization gate
// (commandrisk severity is data fed to it, never a gate by itself), so every
// guard test below that needs a "deny" wires this in rather than relying on
// a nil enforcer.
func denyCommandExecPolicy(t *testing.T) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "deny.rego", `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if input.action == "command_exec"
deny_reason := "command_exec blocked by test policy" if input.action == "command_exec"
`)
	require.NoError(t, err)
	return enf
}

// TestPtyProxyRunBridge_OperatorGuardBlocksDenied is the FIX-2 regression:
// web.guard_mode must gate the operator's normal ptmx path too (the mux path
// handleWebPtyProxy/handleWebInterceptResume take, since the web UI always
// sends a session_id) — not just the collaborate-guest relay.
func TestPtyProxyRunBridge_OperatorGuardBlocksDenied(t *testing.T) {
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
		guard := termGuardInputs{Enforcer: denyCommandExecPolicy(t), Actor: "alice", Record: hosts.Record{Name: "target1"}, Mode: termguard.ModeEnforce}
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_operator_guard_test", closeTabKill,
			ptyProxyStdinPolicy{OperatorGuard: &guard})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte(dangerousDeletePath()+"\r")))

	buf := make([]byte, 64)
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, rerr := peer.Read(buf)
	require.NoError(t, rerr)
	require.NotContains(t, string(buf[:n]), "\r", "a denied command's Enter must never reach the operator's own ptmx")
	require.Contains(t, buf[:n], byte(0x15), "a denied command's Enter must be replaced with Ctrl-U")

	_ = ptmx.Close()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}

// TestPtyProxyRunBridge_OperatorGuardOffByteIdentical proves the honest side
// of FIX-2's ruling: with GuardMode off (the default), the operator's ptmx
// path stays byte-identical to no wrap at all — newGuardRelay's own ModeOff
// fast path never touches the guard machinery.
func TestPtyProxyRunBridge_OperatorGuardOffByteIdentical(t *testing.T) {
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
		guard := termGuardInputs{Mode: termguard.ModeOff}
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_operator_guard_off_test", closeTabKill,
			ptyProxyStdinPolicy{OperatorGuard: &guard})
		close(done)
	}))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	payload := []byte(dangerousDeletePath() + "\r")
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, payload))

	buf := make([]byte, 64)
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, rerr := peer.Read(buf)
	require.NoError(t, rerr)
	require.Equal(t, payload, buf[:n], "guard_mode off must leave the operator's ptmx path byte-identical")

	_ = ptmx.Close()
	_ = conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ptyProxyRunBridge did not return after ptmx/conn were closed")
	}
}
