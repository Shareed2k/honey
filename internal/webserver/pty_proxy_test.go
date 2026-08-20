package webserver

import (
	"bytes"
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
			restore := swapTmuxRun(func(args ...string) ([]byte, error) {
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

// TestTmuxSendKeysHex_ChunksLargePayload proves a burst larger than
// maxSendKeysHexArgsPerExec is chunked into multiple bounded execs instead of
// one unbounded argv/subprocess.
func TestTmuxSendKeysHex_ChunksLargePayload(t *testing.T) {
	payload := bytes.Repeat([]byte{0x41}, maxSendKeysHexArgsPerExec+10)

	var calls [][]string
	restore := swapTmuxRun(func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		return nil, nil
	})
	defer restore()

	require.NoError(t, tmuxSendKeysHex("honey_target:", payload))
	require.Len(t, calls, 2, "a payload over the per-exec bound must chunk into multiple execs")
	require.Len(t, calls[0], 4+maxSendKeysHexArgsPerExec)
	require.Len(t, calls[1], 4+10)
}

// TestTmuxSendKeysHex_PropagatesRunError proves a tmux failure surfaces as a
// wrapped error instead of being silently swallowed.
func TestTmuxSendKeysHex_PropagatesRunError(t *testing.T) {
	restore := swapTmuxRun(func(...string) ([]byte, error) {
		return []byte("can't find pane"), errors.New("exit status 1")
	})
	defer restore()

	err := tmuxSendKeysHex("honey_target:", []byte("x"))
	require.Error(t, err)
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
	restore := swapTmuxRun(func(args ...string) ([]byte, error) {
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
