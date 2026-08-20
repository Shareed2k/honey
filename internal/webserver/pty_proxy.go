package webserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/engine"
	"go.uber.org/zap"
)

// ptyMuxSessionName builds a tmux/zellij session name from the client-provided id.
// Only safe ASCII characters are kept; if nothing remains, a short stable digest is used
// so multiplexer argv never embed raw untrusted strings (gosec G204).
func ptyMuxSessionName(sessionID string) string {
	const prefix = "honey_"
	s := strings.TrimSpace(sessionID)
	var b strings.Builder
	b.Grow(len(prefix) + 64)
	b.WriteString(prefix)
	for i := 0; i < len(s) && b.Len() < len(prefix)+64; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteByte(c)
		}
	}
	if b.Len() == len(prefix) {
		sum := sha256.Sum256([]byte(s))
		b.WriteString(hex.EncodeToString(sum[:8]))
	}
	return b.String()
}

// ptyWinsize clamps terminal dimensions to a valid pty.Winsize range (defaults match handleWebSSH).
func ptyWinsize(cols, rows int) pty.Winsize {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	if cols > math.MaxUint16 {
		cols = math.MaxUint16
	}
	if rows > math.MaxUint16 {
		rows = math.MaxUint16
	}
	return pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
}

// ptyProxyExecArgs builds the argv tmux/zellij run as the pane command: the
// binary first (see os.Executable()), then sub ("pty-proxy" or
// "intercept-pane"), an optional --config, and the base64 payload last.
func ptyProxyExecArgs(sub, bin, configPath, encodedPayload string) []string {
	args := []string{bin, sub}
	if strings.TrimSpace(configPath) != "" {
		args = append(args, "--config", configPath)
	}
	args = append(args, encodedPayload)
	return args
}

// ptyMuxBuildCommand returns a zellij/tmux attach-or-create command for the session id.
func ptyMuxBuildCommand(bin, configPath, encodedPayload, sessionID string) (cmd *exec.Cmd, muxName string, useZellij bool, err error) {
	muxName = ptyMuxSessionName(sessionID)
	proxyArgs := ptyProxyExecArgs("pty-proxy", bin, configPath, encodedPayload)
	if _, err := exec.LookPath("zellij"); err == nil {
		return ptyMuxZellijCommand(muxName, proxyArgs)
	}
	if _, err := exec.LookPath("tmux"); err == nil {
		return ptyMuxTmuxCommand(muxName, proxyArgs, attachExclusive)
	}
	zap.L().Debug("handleWebPtyProxy: no multiplexer found, falling back")
	return nil, muxName, false, fmt.Errorf("neither zellij nor tmux found on the server")
}

// interceptPaneMuxName derives a deterministic, per-pod mux session name for
// the intercept resume path from a digest of cluster/namespace/pod, not the
// raw fields themselves: a browser refresh recomputes the identical name for
// the same pod, which is how it re-attaches to its own pane, and a later task
// lists live panes by the "honey-int-" prefix. It intentionally does NOT
// route through ptyMuxSessionName's "honey_" wrapping: the name is already
// exec-argv-safe (fixed literal + hex digest, never raw record fields), and
// the tmux-registry task must find sessions by their literal "honey-int-"
// prefix. One consequence: ptyMuxTmuxCommand's validHoneyMuxSessionName-gated
// fast paths (tmuxSessionAlive/tmuxHasSession/respawn-dead-pane) don't fire
// for this name family, so attach-or-create for tmux falls through to its
// default branch — which still works because `tmux new-session -A -D`
// attaches to an existing session by its real name regardless.
func interceptPaneMuxName(cluster, namespace, pod string) string {
	sum := sha256.Sum256([]byte(cluster + "\x00" + namespace + "\x00" + pod))
	return "honey-int-" + hex.EncodeToString(sum[:])[:16]
}

// ptyMuxBuildInterceptCommand mirrors ptyMuxBuildCommand for the intercept
// resume path: it takes an already-computed mux name (interceptPaneMuxName)
// instead of sanitizing a client-supplied session id, and builds the
// intercept-pane argv instead of pty-proxy's. It is tmux-ONLY (never zellij):
// the resume list/cap/stop are tmux-based, so a zellij-hosted pane could not be
// managed. useZellij is therefore always false. The caller gates on tmuxOnPath,
// so the LookPath here is defense-in-depth.
func ptyMuxBuildInterceptCommand(bin, configPath, encodedPayload, name string) (cmd *exec.Cmd, muxName string, useZellij bool, err error) {
	proxyArgs := ptyProxyExecArgs("intercept-pane", bin, configPath, encodedPayload)
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, name, false, fmt.Errorf("intercept resume requires tmux on the server: %w", err)
	}
	return ptyMuxTmuxCommand(name, proxyArgs, attachExclusive)
}

func ptyMuxZellijCommand(muxName string, proxyArgs []string) (*exec.Cmd, string, bool, error) {
	pruneHoneyZellijSessions(muxName)
	if zellijSessionAlive(muxName) {
		cmd := exec.Command("zellij", "attach", muxName) // #nosec G204 -- muxName sanitized
		zap.L().Debug("handleWebPtyProxy: zellij attach existing", zap.String("session", muxName))
		return cmd, muxName, true, nil
	}
	zellijArgs := append([]string{"attach", "-c", "-f", muxName, "--"}, proxyArgs...)
	cmd := exec.Command("zellij", zellijArgs...) // #nosec G204 -- see comment above
	zap.L().Debug("handleWebPtyProxy: zellij create", zap.Strings("args", cmd.Args))
	return cmd, muxName, true, nil
}

// attachMode selects how ptyMuxTmuxCommand joins a tmux session. It exists so
// a share-link GUEST — authenticated only by a redeemed JIT grant, never by
// the shared web token — can be attached to the operator's live session
// without ever gaining the power to detach the operator or conjure a session
// that doesn't already exist.
type attachMode int

const (
	// attachExclusive is the host/refresh path: attach -d (detaching any other
	// client) or new-session -A -D (attach-or-create). UNCHANGED behavior —
	// every caller before this task passes this mode, verbatim.
	attachExclusive attachMode = iota
	// attachShared joins an EXISTING session read-write, alongside the
	// operator's own client (no -d). It never creates or respawns a session.
	attachShared
	// attachReadonly joins an EXISTING session read-only (tmux `-r`), alongside
	// the operator's own client (no -d). It never creates or respawns a session.
	attachReadonly
)

// tmuxGuestSessionAlive reports whether muxName is a live tmux session,
// dispatching to the family-appropriate check (see the two mux families in
// pty_mux.go / intercept_mux.go). It is a package var so tests can simulate an
// alive/dead session without a real tmux server. name has already been
// re-validated by the caller before this runs.
var tmuxGuestSessionAlive = func(name string) bool {
	if validHoneyMuxSessionName(name) {
		return tmuxSessionAlive(name)
	}
	if validInterceptMuxName(name) {
		return tmuxHasInterceptSession(name)
	}
	return false
}

func ptyMuxTmuxCommand(muxName string, proxyArgs []string, mode attachMode) (*exec.Cmd, string, bool, error) {
	if mode != attachExclusive {
		return ptyMuxTmuxGuestAttach(muxName, mode)
	}
	pruneHoneyTmuxSessions(muxName)
	switch {
	case tmuxSessionAlive(muxName):
		cmd := exec.Command("tmux", "attach", "-d", "-t", muxName) // #nosec G204 -- muxName sanitized
		zap.L().Debug("handleWebPtyProxy: tmux attach reuse", zap.String("session", muxName))
		return cmd, muxName, false, nil
	case tmuxHasSession(muxName):
		if err := tmuxRespawnPane(muxName, proxyArgs); err != nil {
			zap.L().Warn("handleWebPtyProxy: tmux respawn-pane failed, recreating session", zap.String("session", muxName), zap.Error(err))
			tmuxKillSession(muxName)
			tmuxArgs := append([]string{"new-session", "-A", "-D", "-s", muxName}, proxyArgs...)
			cmd := exec.Command("tmux", tmuxArgs...) // #nosec G204 -- see comment above
			zap.L().Debug("handleWebPtyProxy: tmux create", zap.Strings("args", cmd.Args))
			return cmd, muxName, false, nil
		}
		cmd := exec.Command("tmux", "attach", "-d", "-t", muxName) // #nosec G204 -- muxName sanitized
		zap.L().Debug("handleWebPtyProxy: tmux respawn and attach", zap.String("session", muxName))
		return cmd, muxName, false, nil
	default:
		tmuxArgs := append([]string{"new-session", "-A", "-D", "-s", muxName}, proxyArgs...)
		cmd := exec.Command("tmux", tmuxArgs...) // #nosec G204 -- see comment above
		zap.L().Debug("handleWebPtyProxy: tmux create", zap.Strings("args", cmd.Args))
		return cmd, muxName, false, nil
	}
}

// ptyMuxTmuxGuestAttach builds the attachShared/attachReadonly command for a
// guest joining an operator's live session. Unlike attachExclusive it NEVER
// creates or respawns a session — a guest that guessed or was handed a
// mux_session for a session that has already ended must get an error, never a
// freshly conjured session masquerading as the one it was granted. The name is
// re-validated here, immediately before it reaches a tmux argv, so the
// "#nosec G204 -- muxName sanitized" invariant holds for this call path too,
// independent of whatever validated it at grant-create time.
func ptyMuxTmuxGuestAttach(muxName string, mode attachMode) (*exec.Cmd, string, bool, error) {
	if !validHoneyMuxSessionName(muxName) && !validInterceptMuxName(muxName) {
		return nil, muxName, false, fmt.Errorf("invalid mux session name %q", muxName)
	}
	if !tmuxGuestSessionAlive(muxName) {
		return nil, muxName, false, fmt.Errorf("shared session %q has ended", muxName)
	}
	args := []string{"attach", "-t", muxName}
	if mode == attachReadonly {
		args = []string{"attach", "-r", "-t", muxName}
	}
	cmd := exec.Command("tmux", args...) // #nosec G204 -- muxName sanitized
	zap.L().Debug("handleWebPtyProxy: tmux guest attach", zap.String("session", muxName), zap.Bool("readonly", mode == attachReadonly))
	return cmd, muxName, false, nil
}

// ptyProxyRunBridge pipes ptmx<->conn until either side closes. readOnly is
// true only for a share-link guest holding a "watch" grant: it drops every
// BinaryMessage (stdin) frame instead of writing it to ptmx, so a watch guest
// cannot type into the session even if the client were compromised — belt and
// braces alongside tmux's own `-r` attach flag, which is the primary
// enforcement (see ptyMuxTmuxGuestAttach). Every pre-existing caller passes
// false, keeping their behavior byte-identical.
func ptyProxyRunBridge(
	ptmx *os.File,
	conn *websocket.Conn,
	recorder *engine.SessionRecorder,
	hello WSHello,
	muxName string,
	closeTabKill chan struct{},
	readOnly bool,
) chan struct{} {
	wsOut := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	defer bridgeCancel()

	ws := ptyWinsize(hello.Cols, hello.Rows)
	if err := pty.Setsize(ptmx, &ws); err != nil {
		zap.L().Warn("failed to resize pty", zap.Error(err))
	}

	var wg sync.WaitGroup
	wg.Add(2)

	ptyExited := make(chan struct{})

	go func() {
		defer wg.Done()
		innerExited := false
		defer func() {
			if innerExited {
				close(ptyExited)
			}
		}()
		buf := make([]byte, 4096)
		for {
			select {
			case <-bridgeCtx.Done():
				return
			default:
			}
			n, err := ptmx.Read(buf)
			if n > 0 {
				out := buf[:n]
				recorder.RecordData("stdout", out)
				if _, werr := wsOut.Write(out); werr != nil {
					bridgeCancel()
					return
				}
			}
			if err != nil {
				if bridgeCtx.Err() == nil {
					innerExited = true
				}
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		defer bridgeCancel()
		for {
			mt, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if readOnly {
					// A watch guest: never write to the shared pty. tmux's `-r`
					// attach already enforces this server-side; dropping the
					// frame here too means our own code path never even
					// attempts the write.
					continue
				}
				recorder.RecordData("stdin", payload)
				if _, werr := ptmx.Write(payload); werr != nil {
					return
				}
			case websocket.TextMessage:
				if ptyProxyHandleCtrl(ptmx, recorder, muxName, closeTabKill, payload) {
					return
				}
			}
		}
	}()

	wg.Wait()
	_ = ptmx.SetReadDeadline(time.Now())
	return ptyExited
}

func ptyProxyHandleCtrl(ptmx *os.File, recorder *engine.SessionRecorder, muxName string, closeTabKill chan struct{}, payload []byte) (stop bool) {
	var ctrl struct {
		Type string `json:"type"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}
	if json.Unmarshal(payload, &ctrl) != nil {
		return false
	}
	switch ctrl.Type {
	case "resize":
		if ctrl.Cols > 0 && ctrl.Rows > 0 {
			recorder.RecordResize(ctrl.Cols, ctrl.Rows)
			ws := ptyWinsize(ctrl.Cols, ctrl.Rows)
			_ = pty.Setsize(ptmx, &ws)
		}
	case "detach":
		return true
	case "close_tab":
		zap.L().Debug("handleWebPtyProxy: close_tab received", zap.String("session", muxName))
		select {
		case closeTabKill <- struct{}{}:
		default:
		}
		return true
	}
	return false
}

// ptyProxyTeardown ends one pty-proxy bridge. killSession is the explicit
// close_tab (×) kill: the SSH path passes the honey_* mux killer, the intercept
// resume path passes its own honey-int-* killer (the honey_* helpers gate on
// validHoneyMuxSessionName and are inert for that name family).
func ptyProxyTeardown(ptmx *os.File, cmd *exec.Cmd, muxName string, useZellij bool, closeTabKill, ptyExited chan struct{}, killSession func()) {
	select {
	case <-ptyExited:
		_ = ptmx.Close()
		reapPtyProxyCmd(cmd)
		ptyMuxKillSessionIfExited(muxName, useZellij)
	default:
		_ = ptmx.Close()
		select {
		case <-closeTabKill:
			zap.L().Debug("handleWebPtyProxy: killing mux session after close_tab", zap.String("session", muxName))
			reapPtyProxyCmd(cmd)
			killSession()
		default:
			// Plain disconnect (browser refresh/detach): the mux client is left to
			// exit on its own now that its pty master is closed — but it must still
			// be reaped, or every refresh leaks a defunct child until honey-web
			// exits. Waiting in the background because that exit is not immediate.
			go func() { _ = cmd.Wait() }()
		}
	}
}

// reapPtyProxyCmd kills the mux client and reaps it, so no zombie survives the
// teardown. Both callers run after a successful pty.Start, so Process is set;
// the guard is defensive.
func reapPtyProxyCmd(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
}

func handleWebPtyProxy(conn *websocket.Conn, helloRaw []byte, hello WSHello, recorder *engine.SessionRecorder, configPath string) error {
	zap.L().Debug("handleWebPtyProxy: starting local multiplexer", zap.String("session_id", hello.SessionID))

	bin, err := os.Executable()
	if err != nil {
		// Single-handling: caller (ws_ssh) logs at Error; just wrap and return.
		return fmt.Errorf("failed to get executable: %w", err)
	}

	encodedPayload := base64.StdEncoding.EncodeToString(helloRaw)
	cmd, muxName, useZellij, err := ptyMuxBuildCommand(bin, configPath, encodedPayload, hello.SessionID)
	if err != nil {
		return err
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		// Single-handling: caller (ws_ssh) logs at Error; just wrap and return.
		return fmt.Errorf("failed to start pty: %w", err)
	}

	closeTabKill := make(chan struct{}, 1)
	ptyExited := ptyProxyRunBridge(ptmx, conn, recorder, hello, muxName, closeTabKill, false)
	ptyProxyTeardown(ptmx, cmd, muxName, useZellij, closeTabKill, ptyExited, func() { ptyMuxKillSession(muxName, useZellij) })
	return nil
}

// handleLiveTerminalAttach bridges conn to muxSession — an operator's EXISTING
// tmux session — for a share-link guest holding a redeemed live_terminal
// grant. mode must be attachShared (collaborate) or attachReadonly (watch);
// unlike handleWebPtyProxy this path never creates or respawns a session:
// ptyMuxTmuxCommand errors out instead, because a guest must never conjure a
// session it was not actually granted a LIVE counterpart for.
//
// Guest teardown never kills the operator's session: close_tab reaps only the
// guest's own tmux client process (ptyProxyTeardown's killSession is a no-op
// here), and tmux itself keeps a session alive as long as it exists,
// independent of how many clients are attached.
func handleLiveTerminalAttach(conn *websocket.Conn, muxSession string, mode attachMode, cols, rows int, recorder *engine.SessionRecorder) error {
	cmd, _, _, err := ptyMuxTmuxCommand(muxSession, nil, mode)
	if err != nil {
		return err
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start guest attach pty: %w", err)
	}

	hello := WSHello{Cols: cols, Rows: rows}
	closeTabKill := make(chan struct{}, 1)
	readOnly := mode == attachReadonly
	ptyExited := ptyProxyRunBridge(ptmx, conn, recorder, hello, muxSession, closeTabKill, readOnly)
	ptyProxyTeardown(ptmx, cmd, muxSession, false, closeTabKill, ptyExited, func() {})
	return nil
}
