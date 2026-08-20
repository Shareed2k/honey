package webserver

import (
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
// guest attachMode against a live (faked) session: shared = attach -t (no -d,
// no -r); readonly = attach -r -t (no -d). Neither ever builds a new-session
// argv — ptyMuxTmuxGuestAttach has no such branch to begin with.
func TestPtyMuxTmuxCommand_GuestAttachArgv(t *testing.T) {
	tests := []struct {
		name string
		mode attachMode
		want []string
	}{
		{name: "shared attach (collaborate)", mode: attachShared, want: []string{"tmux", "attach", "-t", "honey_guest_argv_test"}},
		{name: "readonly attach (watch)", mode: attachReadonly, want: []string{"tmux", "attach", "-r", "-t", "honey_guest_argv_test"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFakeGuestSessionAlive(t, true)
			cmd, muxName, useZellij, err := ptyMuxTmuxCommand("honey_guest_argv_test", nil, tc.mode)
			require.NoError(t, err)
			require.Equal(t, "honey_guest_argv_test", muxName)
			require.False(t, useZellij)
			require.Equal(t, tc.want, cmd.Args)
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
	// client — never a real tmux kill-session.
	ptyProxyTeardown(ptmx, cmd, name, false, closeTabKill, ptyExited, func() {})

	// reapPtyProxyCmd calls (*os.Process).Kill+Wait, not (*exec.Cmd).Wait, so
	// cmd.ProcessState is never populated here — check liveness directly via a
	// signal-0 probe instead.
	require.Error(t, syscall.Kill(guestPID, 0), "the guest's own tmux attach client must be reaped (still running)")

	// The load-bearing proof: the OPERATOR's session is still alive. If a real
	// kill-session had run (the bug this task fixes), has-session would fail.
	require.NoError(t, exec.Command("tmux", "has-session", "-t", name).Run(), "operator's session must survive a guest close_tab")
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

// TestPtyProxyRunBridge_ReadOnlySkipsGuestStdin proves ptyProxyRunBridge's
// readOnly flag — the watch-grant belt-and-braces guard alongside tmux's own
// `-r` attach — never writes a guest's stdin frame into the pty, and that
// readOnly=false (every pre-existing caller) is unaffected.
func TestPtyProxyRunBridge_ReadOnlySkipsGuestStdin(t *testing.T) {
	t.Run("readOnly=true drops guest stdin", func(t *testing.T) {
		testPtyProxyRunBridgeStdinGate(t, true)
	})
	t.Run("readOnly=false forwards stdin (unchanged)", func(t *testing.T) {
		testPtyProxyRunBridgeStdinGate(t, false)
	})
}

func testPtyProxyRunBridgeStdinGate(t *testing.T, readOnly bool) {
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
		<-ptyProxyRunBridge(ptmx, conn, (*engine.SessionRecorder)(nil), hello, "honey_stdin_gate_test", closeTabKill, readOnly)
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
	if readOnly {
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
