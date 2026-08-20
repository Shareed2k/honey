package webserver

import (
	"bytes"
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
	// attachShared is the collaborate guest mode. Its tmux CLIENT is attached
	// read-only (`-r`), exactly like attachReadonly — a guest client is NEVER
	// given a mutating tmux client, full stop (see the HIGH-1 note below). What
	// makes it "collaborate" is that the guest's keystrokes still reach the
	// pane, via a separate out-of-band `tmux send-keys -H` call
	// (tmuxSendKeysHex) that ptyProxyRunBridge issues instead of writing to
	// this client's ptmx. It never creates or respawns a session.
	attachShared
	// attachReadonly is the watch guest mode: read-only tmux client (`-r`),
	// AND no stdin wired into the bridge at all (see ptyProxyRunBridge) — the
	// guest cannot influence the pane in any way. It never creates or
	// respawns a session.
	attachReadonly
)

// HIGH-1 (ship-blocker, closed by this task): a collaborate guest used to be
// attached with a plain `tmux attach -t <name>` — a FULL tmux client on
// honey-web's own tmux socket, default keybindings and all. On tmux 3.5a, a
// guest merely typing `\x02c` (`C-b c`) opened a brand-new window running a
// local shell ON THE HONEY CONTROL-PLANE HOST: remote code execution for
// anyone holding an unauthenticated share code. `C-b :` (run-shell,
// kill-session) and `C-b s` / `C-b )` (switch to any other honey_*/
// honey-int-* session) were the same hole. Both guest modes now attach `-r`
// (see ptyMuxTmuxGuestAttach) so neither can ever run a tmux command that
// mutates state — note `-r` alone is NOT sufficient (tmux still permits its
// small set of CMD_READONLY commands to a read-only client), which is why a
// collaborate guest's actual keystrokes never reach this client's ptmx at
// all; they are relayed to the pane directly via tmuxSendKeysHex. The
// longer-term correct fix — moving honey's mux to its own socket with
// `prefix none` — was rejected for this task because it relocates every
// existing OPERATOR session (attachExclusive) and touches a path this task
// must leave byte-identical; it remains a real follow-up.

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
	// LOW-5 (round-2 residual): pin the shared window's size BEFORE the guest
	// attaches, so its own (possibly small, possibly hello-lied) terminal size
	// can never shrink the operator's real window — see pinGuestWindowSize.
	pinGuestWindowSize(muxName)
	// HIGH-1: BOTH guest modes attach -r (read-only). A collaborate guest's
	// keystrokes still reach the pane, but never through this client — see
	// tmuxSendKeysHex and the HIGH-1 comment on the attachMode consts above.
	cmd := exec.Command("tmux", "attach", "-r", "-t", muxName) // #nosec G204 -- muxName sanitized
	zap.L().Debug("handleWebPtyProxy: tmux guest attach", zap.String("session", muxName), zap.String("mode", guestAttachModeLabel(mode)))
	return cmd, muxName, false, nil
}

// guestAttachModeLabel names an attachMode for logging.
func guestAttachModeLabel(mode attachMode) string {
	if mode == attachShared {
		return "collaborate"
	}
	return "watch"
}

// pinGuestWindowSize sets tmux's window-size option to "manual" on the
// target session before a guest attaches (LOW-5, round-2 residual): tmux's
// default sizing shrinks a session's shared window to fit the SMALLEST
// attached client's terminal, so a guest attaching at a small size (or one
// that simply lies in its hello frame) would otherwise reshape the
// operator's real window even though resize CONTROL FRAMES are already
// dropped for guests (measured: a 40x10 guest shrank a detached operator's
// 200x50 window to 40x9, and it stayed that way). Best-effort: a failure
// here still leaves the -r attach itself safe, just not pinned, so it never
// blocks the attach.
func pinGuestWindowSize(muxName string) {
	if _, err := tmuxRun("set-option", "-t", muxName, "window-size", "manual"); err != nil {
		zap.L().Debug("pty_proxy: could not pin window-size for guest attach", zap.String("session", muxName), zap.Error(err))
	}
}

// tmuxCanonicalSessionName resolves name to tmux's OWN idea of the target
// session's name via `tmux display-message -p -t <name> '#{session_name}'`,
// and rejects anything but an exact match (NEW-3). tmux matches a `-t`
// target by PREFIX — has-session/show-environment/send-keys/attach all treat
// "honey-int-abc" as a match for a real "honey-int-abcdef" session — so a
// caller naming a unique prefix would otherwise pass every validity/liveness/
// ownership check and attach to the REAL session while policy input and the
// audit trail only ever recorded the ALIAS: an exact-match policy rule is
// evadable, and an investigator searching the audit log for the real session
// name would never find this grant. name must already be format-validated
// (validHoneyMuxSessionName / validInterceptMuxName) before this runs, same
// invariant as everywhere else a name reaches a tmux argv.
func tmuxCanonicalSessionName(name string) (string, error) {
	out, err := tmuxRun("display-message", "-p", "-t", name, "#{session_name}")
	if err != nil {
		return "", fmt.Errorf("resolve session %q: %w", name, err)
	}
	canonical := strings.TrimSpace(string(out))
	if canonical == "" || canonical != name {
		return "", fmt.Errorf("mux_session %q is not an exact tmux session name", name)
	}
	return canonical, nil
}

// maxRelayFrameBytes bounds a single collaborate-guest WebSocket frame that
// tmuxSendKeysHex will ever relay (NEW-5, round 2): each byte becomes one
// hex arg to a single `send-keys` exec, and a real tmux fork costs ~10ms —
// an unbounded frame from an unauthenticated share-code holder (measured: a
// 10 MiB frame ⇒ ~20 480 forks, ~3.5 minutes) would hammer the ONE shared
// tmux server hosting every operator session. A frame over the cap is
// rejected whole (see the Judgment note on tmuxSendKeysHex below) rather
// than relayed in bounded pieces.
const maxRelayFrameBytes = 512

// tmuxSendKeysRunTimeout bounds one tmuxSendKeysHex exec (NEW-1, round 2): a
// guest-delivered byte can map to a `command-prompt` binding in the target
// pane's CURRENT tmux mode table (e.g. the operator scrolls into copy-mode,
// then the guest types `:`/`f`/`t`/`g` — all bound to command-prompt in the
// default emacs copy-mode table) and `send-keys` blocks indefinitely
// waiting on that prompt. It is a var, not a const, so a test can shrink it
// without a multi-second sleep.
var tmuxSendKeysRunTimeout = 2 * time.Second

// tmuxRunBounded is tmuxSendKeysHex's relay-local exec seam: unlike the
// shared tmuxRun (intercept_mux.go — no timeout, and used by cheap,
// synchronous, never-guest-triggered calls where a hang would itself be a
// loud bug worth surfacing), this ALWAYS bounds tmux with a context
// deadline, because a guest-controlled byte is the trigger here (NEW-1). It
// is a package var so tests can fake it without a real tmux server or a
// real multi-second wait. exec.CommandContext kills the child the instant
// the deadline fires, so no tmux process is left to leak.
var tmuxRunBounded = func(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxSendKeysRunTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput() // #nosec G204 -- fixed verbs; target/hex args validated/generated, never raw guest bytes
	if err != nil && ctx.Err() != nil {
		return out, fmt.Errorf("tmux command timed out after %s: %w", tmuxSendKeysRunTimeout, ctx.Err())
	}
	return out, err
}

// tmuxSendKeysHex is the HIGH-1 mediation seam: it relays a collaborate
// guest's raw keystroke bytes to target (a pre-validated tmux target, e.g.
// "<session>:") out-of-band via `tmux send-keys -H <hex> <hex> ...` — one
// two-digit hex argument per byte, generated here, NEVER the raw bytes
// themselves as an argv string. This is the only way a collaborate guest's
// input ever reaches the pane: its own tmux client is attached read-only
// (ptyMuxTmuxGuestAttach), so send-keys is issued against the session/pane
// directly, never through that client. Because every guest byte now passes
// through this one function, it is also the seam a later command-policy task
// wraps to filter guest keystrokes before they reach argv.
//
// Judgment (round 2): this is deliberately ONE exec for the WHOLE payload,
// never chunked — round 1's per-512-byte chunking loop could half-apply a
// larger paste (an earlier chunk's newline-terminated commands would already
// have executed by the time a later chunk failed, with the rest silently
// discarded and the guest never told). All-or-nothing per frame is safer: an
// oversized payload is refused outright, before any tmux exec at all, so a
// paste either reaches the pane whole or not at all.
func tmuxSendKeysHex(target string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	if len(payload) > maxRelayFrameBytes {
		return fmt.Errorf("relay frame of %d bytes exceeds the %d-byte cap", len(payload), maxRelayFrameBytes)
	}
	args := make([]string, 0, len(payload)+4)
	args = append(args, "send-keys", "-H", "-t", target)
	for _, b := range payload {
		args = append(args, fmt.Sprintf("%02x", b))
	}
	if _, err := tmuxRunBounded(args...); err != nil {
		return fmt.Errorf("relay keystrokes to %q: %w", target, err)
	}
	return nil
}

// filterTerminalReports strips terminal-REPORT escape sequences from a
// collaborate guest's relayed bytes (NEW-2): a real terminal answers certain
// queries automatically — Device Attributes (DA1 "\x1b[c", DA2 "\x1b[>c"),
// Cursor Position Report (CPR, reply "\x1b[<row>;<col>R"), and OSC color
// queries (reply e.g. "\x1b]11;rgb:.../\x07") — and the guest's browser
// (xterm.js) does exactly that whenever the pane's mirrored output contains
// such a query, sending the reply right back through the SAME onData path
// the guest's own typing uses. Because every guest byte is relayed into the
// pane, an unfiltered reply would land as literal text at the OPERATOR's
// shell prompt (executable if Enter follows) or duplicate a genuine CPR
// reply an app in the pane is waiting on exactly once, corrupting a TUI.
// Ordinary typed bytes — letters, editing control chars, arrow/function-key
// CSI sequences like "\x1b[A" — are untouched; only the specific reply
// SHAPES below are recognized and dropped.
func filterTerminalReports(payload []byte) []byte {
	out := make([]byte, 0, len(payload))
	for i := 0; i < len(payload); {
		if n := terminalReportLen(payload[i:]); n > 0 {
			i += n
			continue
		}
		out = append(out, payload[i])
		i++
	}
	return out
}

// terminalReportLen returns the length of a terminal-report reply at the
// start of b, or 0 if b does not begin with one.
func terminalReportLen(b []byte) int {
	switch {
	case bytes.HasPrefix(b, []byte("\x1b[")):
		// CSI ... final-byte: a report reply's final byte is one of c/R/n
		// (device attributes / cursor position / other device-status
		// reports). An ordinary CSI a human types (arrow keys, Home/End,
		// function keys, ...) ends in a DIFFERENT final byte (A/B/C/D/H/F/~/
		// ...) and is left alone.
		for i := 2; i < len(b); i++ {
			c := b[i]
			if c >= 0x40 && c <= 0x7e { // CSI final-byte range
				if c == 'c' || c == 'R' || c == 'n' {
					return i + 1
				}
				return 0
			}
		}
		return 0
	case bytes.HasPrefix(b, []byte("\x1b]")), bytes.HasPrefix(b, []byte("\x1bP")):
		// OSC ("\x1b]...") / DCS ("\x1bP...") replies, terminated by BEL
		// (\x07) or ST ("\x1b\\").
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2
			}
		}
		return 0
	default:
		return 0
	}
}

// ptyProxyStdinPolicy controls how ptyProxyRunBridge handles inbound
// WebSocket stdin/control traffic. The zero value is the pre-existing
// operator/non-guest behavior — stdin forwarded straight to ptmx, resize
// honored — so every pre-existing caller passing the zero value keeps
// byte-identical behavior.
type ptyProxyStdinPolicy struct {
	// DropStdin discards every BinaryMessage frame instead of writing it
	// anywhere. Set only for a watch guest: on top of tmux's own `-r` attach
	// (defense in depth, not the primary control — see the HIGH-1 comment on
	// the attachMode consts), this means our own code never even attempts a
	// write for that guest.
	DropStdin bool
	// RelayTarget, set only for a collaborate guest, is a pre-validated tmux
	// target ("<session>:") that inbound bytes are relayed to out-of-band via
	// tmuxSendKeysHex — never written to this connection's ptmx (see HIGH-1).
	RelayTarget string
	// IgnoreResize (LOW-5) means this connection never drives window sizing at
	// all: neither a later "resize" control frame NOR the very first hello
	// cols/rows (round-2 residual — a guest's hello was still reaching
	// pty.Setsize even though resize FRAMES were already dropped; measured: a
	// 40x10 guest shrank a detached operator's 200x50 window to 40x9 and it
	// stayed). Set for both guest modes — the operator alone drives sizing,
	// full stop. pinGuestWindowSize (tmux `window-size manual`, set before the
	// guest's tmux client even attaches) is the belt to this brace: it pins
	// the SHARED window server-side regardless of what any client's local pty
	// size claims to be.
	IgnoreResize bool
}

// ptyProxyRunBridge pipes ptmx<->conn until either side closes. stdin
// controls how inbound guest bytes are handled (see ptyProxyStdinPolicy); the
// zero value keeps every pre-existing caller byte-identical.
func ptyProxyRunBridge(
	ptmx *os.File,
	conn *websocket.Conn,
	recorder *engine.SessionRecorder,
	hello WSHello,
	muxName string,
	closeTabKill chan struct{},
	stdin ptyProxyStdinPolicy,
) chan struct{} {
	wsOut := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	bridgeCtx, bridgeCancel := context.WithCancel(context.Background())
	defer bridgeCancel()

	if !stdin.IgnoreResize {
		ws := ptyWinsize(hello.Cols, hello.Rows)
		if err := pty.Setsize(ptmx, &ws); err != nil {
			zap.L().Warn("failed to resize pty", zap.Error(err))
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)

	ptyExited := make(chan struct{})

	// LOW-7: a plain disconnect (browser tab closed, network dropped) makes
	// the conn-reading goroutine below return and cancel bridgeCtx, but the
	// ptmx-reading goroutine only checks bridgeCtx between reads — if it is
	// currently blocked in ptmx.Read on an idle session (nothing to read),
	// that check never runs again until some other byte eventually arrives,
	// leaving a guest's own tmux client attached to the OPERATOR's session
	// indefinitely. This watcher force-expires that blocked Read the instant
	// the bridge is cancelled, so the guest's client detaches promptly.
	go func() {
		<-bridgeCtx.Done()
		_ = ptmx.SetReadDeadline(time.Now())
	}()

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
				switch {
				case stdin.DropStdin:
					// A watch guest: never deliver its bytes anywhere.
					continue
				case stdin.RelayTarget != "":
					// A collaborate guest: relay out-of-band via send-keys,
					// never write to this connection's (read-only) ptmx.
					// NEW-2: strip terminal-report replies (the browser's own
					// xterm.js answering DA1/DA2/CPR/OSC-color queries exactly
					// like a real terminal would) before they ever reach the
					// pane — see filterTerminalReports.
					filtered := filterTerminalReports(payload)
					if len(filtered) == 0 {
						continue
					}
					// NEW-6: record only what the pane actually received. A
					// failed relay (timeout, oversized frame, tmux error)
					// records the DROP instead of falsely claiming the bytes
					// arrived, and — per the round-2 judgment that a guest
					// typing into a void will blindly retype, possibly a
					// destructive command — tells the guest over the socket so
					// they know to retype rather than assume it landed.
					if err := tmuxSendKeysHex(stdin.RelayTarget, filtered); err != nil {
						zap.L().Warn("ptyProxyRunBridge: relay guest keystrokes failed", zap.Error(err))
						recorder.RecordError(fmt.Errorf("dropped %d guest keystroke byte(s): %w", len(filtered), err))
						_ = wsOut.writeText(`{"error":"your last input could not be delivered and was dropped — please retype"}`)
						continue
					}
					recorder.RecordData("stdin", filtered)
				default:
					recorder.RecordData("stdin", payload)
					if _, werr := ptmx.Write(payload); werr != nil {
						return
					}
				}
			case websocket.TextMessage:
				if ptyProxyHandleCtrl(ptmx, recorder, muxName, closeTabKill, payload, stdin.IgnoreResize) {
					return
				}
			}
		}
	}()

	wg.Wait()
	_ = ptmx.SetReadDeadline(time.Now())
	return ptyExited
}

// ptyProxyHandleCtrl handles one JSON control frame. ignoreResize drops a
// "resize" frame outright (LOW-5, guest paths): detach/close_tab are always
// honored, since neither lets a guest touch the operator's session.
func ptyProxyHandleCtrl(ptmx *os.File, recorder *engine.SessionRecorder, muxName string, closeTabKill chan struct{}, payload []byte, ignoreResize bool) (stop bool) {
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
		if ignoreResize {
			return false
		}
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
// validHoneyMuxSessionName and are inert for that name family). guestPath is
// true only for a share-link guest's own attach client (LOW-6): it skips
// ptyMuxKillSessionIfExited even on a natural ptyExited, so a guest bridge can
// NEVER be the one to reap the operator's session — not through the explicit
// close_tab (×) branch (already a no-op killSession there) and not through
// this "all panes exited" cleanup either. The invariant is absolute, not
// scoped to one teardown branch.
func ptyProxyTeardown(ptmx *os.File, cmd *exec.Cmd, muxName string, useZellij bool, closeTabKill, ptyExited chan struct{}, killSession func(), guestPath bool) {
	select {
	case <-ptyExited:
		_ = ptmx.Close()
		reapPtyProxyCmd(cmd)
		if !guestPath {
			ptyMuxKillSessionIfExited(muxName, useZellij)
		}
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
	ptyExited := ptyProxyRunBridge(ptmx, conn, recorder, hello, muxName, closeTabKill, ptyProxyStdinPolicy{})
	ptyProxyTeardown(ptmx, cmd, muxName, useZellij, closeTabKill, ptyExited, func() { ptyMuxKillSession(muxName, useZellij) }, false)
	return nil
}

// guestReadLimitBytes bounds a single WebSocket message from a share-link
// guest (NEW-5, round 2): with no limit, an unauthenticated share-code
// holder could send one giant frame that honey-web fully buffers in its own
// heap and then, for a collaborate guest, feeds toward tmuxSendKeysHex —
// which additionally caps a single relay at maxRelayFrameBytes, so anything
// this large is rejected long before any tmux exec. 64KiB comfortably covers
// a real paste while keeping a worst-case frame nowhere near the measured
// 10 MiB / ~20 480-fork scenario.
const guestReadLimitBytes = 64 * 1024

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
	// NEW-7: fail closed on any mode this function doesn't know, BEFORE
	// starting any process — the stdin-policy switch below has no default of
	// its own, and its zero value (DropStdin=false, RelayTarget="") is the
	// dangerous one: it would forward a guest's raw bytes straight into
	// ptmx, restoring the exact HIGH-1 hole for a caller that ever passes
	// anything but these two modes. Unreachable today (the sole caller,
	// jit_redeem_ws.go, only ever passes these two) — this is purely
	// defense in depth against a careless future caller.
	switch mode {
	case attachReadonly, attachShared:
	default:
		return fmt.Errorf("handleLiveTerminalAttach: unsupported attach mode %d", mode)
	}

	cmd, _, _, err := ptyMuxTmuxCommand(muxSession, nil, mode)
	if err != nil {
		return err
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("start guest attach pty: %w", err)
	}

	// NEW-5: bound every subsequent frame from this guest connection before
	// the bridge starts reading any of them.
	conn.SetReadLimit(guestReadLimitBytes)

	hello := WSHello{Cols: cols, Rows: rows}
	closeTabKill := make(chan struct{}, 1)
	// IgnoreResize (LOW-5): neither guest mode drives the shared window's
	// size. DropStdin (watch) / RelayTarget (collaborate) implement HIGH-1 —
	// see ptyProxyStdinPolicy and the attachMode consts. Defaulting to
	// DropStdin=true (rather than leaving the zero value) means even an
	// impossible third branch here fails SAFE, not open — belt to the
	// upfront switch's suspenders (NEW-7).
	stdin := ptyProxyStdinPolicy{IgnoreResize: true, DropStdin: true}
	if mode == attachShared {
		// "<session>:" targets the session's active window/pane — muxSession
		// is already validated (ptyMuxTmuxCommand/ptyMuxTmuxGuestAttach ran
		// above and returned no error), so nothing guest-supplied reaches this
		// target string.
		stdin.DropStdin = false
		stdin.RelayTarget = muxSession + ":"
	}
	ptyExited := ptyProxyRunBridge(ptmx, conn, recorder, hello, muxSession, closeTabKill, stdin)
	ptyProxyTeardown(ptmx, cmd, muxSession, false, closeTabKill, ptyExited, func() {}, true)
	return nil
}
