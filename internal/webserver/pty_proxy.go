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
	"strconv"
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
		return ptyMuxTmuxCommand(muxName, proxyArgs)
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

// browserTermType is the terminal type honey declares when it spawns a tmux
// client whose far end is xterm.js in a browser, not the operator's own
// terminal.
//
// It has to be set explicitly: a tmux client inherits TERM from whatever
// started `honey web`, and a systemd unit, container, or launchd job has none
// (or TERM=dumb). tmux then refuses the attach outright — "open terminal
// failed: terminal does not support clear" — which takes down every
// mux-backed web terminal: the guest's share shell, the operator's read-only
// watch, and the intercept terminal alike. The browser end is an xterm
// emulator, so this is also the honest value rather than a guess at the
// operator's environment.
//
// It does assume the host's terminfo database has an xterm-256color entry;
// that holds on macOS and every mainstream distro image, but a stripped
// container with no terminfo at all cannot host a tmux client regardless of
// what TERM says.
const browserTermType = "xterm-256color"

// withBrowserTerm forces TERM on a tmux client honey spawns for a browser
// terminal. exec.Cmd resolves duplicate keys to the LAST value, so appending
// overrides an inherited TERM (including a useless one) without rebuilding
// the environment.
func withBrowserTerm(cmd *exec.Cmd) *exec.Cmd {
	cmd.Env = append(cmd.Environ(), "TERM="+browserTermType)
	return cmd
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
	return ptyMuxTmuxCommand(name, proxyArgs)
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

func ptyMuxTmuxCommand(muxName string, proxyArgs []string) (*exec.Cmd, string, bool, error) {
	pruneHoneyTmuxSessions(muxName)
	switch {
	case tmuxSessionAlive(muxName):
		cmd := withBrowserTerm(exec.Command("tmux", "attach", "-d", "-t", muxName)) // #nosec G204 -- muxName sanitized
		zap.L().Debug("handleWebPtyProxy: tmux attach reuse", zap.String("session", muxName))
		return cmd, muxName, false, nil
	case tmuxHasSession(muxName):
		if err := tmuxRespawnPane(muxName, proxyArgs); err != nil {
			zap.L().Warn("handleWebPtyProxy: tmux respawn-pane failed, recreating session", zap.String("session", muxName), zap.Error(err))
			tmuxKillSession(muxName)
			tmuxArgs := append([]string{"new-session", "-A", "-D", "-s", muxName}, proxyArgs...)
			cmd := withBrowserTerm(exec.Command("tmux", tmuxArgs...)) // #nosec G204 -- see comment above
			zap.L().Debug("handleWebPtyProxy: tmux create", zap.Strings("args", cmd.Args))
			return cmd, muxName, false, nil
		}
		cmd := withBrowserTerm(exec.Command("tmux", "attach", "-d", "-t", muxName)) // #nosec G204 -- muxName sanitized
		zap.L().Debug("handleWebPtyProxy: tmux respawn and attach", zap.String("session", muxName))
		return cmd, muxName, false, nil
	default:
		tmuxArgs := append([]string{"new-session", "-A", "-D", "-s", muxName}, proxyArgs...)
		cmd := withBrowserTerm(exec.Command("tmux", tmuxArgs...)) // #nosec G204 -- see comment above
		zap.L().Debug("handleWebPtyProxy: tmux create", zap.Strings("args", cmd.Args))
		return cmd, muxName, false, nil
	}
}

// shareGuestSessionID is ptyMuxSessionName's input for a redeemed
// access-request's guest shell: "share_" + the grant id. ptyMuxSessionName
// turns this into the actual honey_* tmux session name a second, read-only
// client (the operator's watch route) can later attach to — see
// shareGuestMuxName.
func shareGuestSessionID(grantID string) string {
	return "share_" + grantID
}

// shareGuestMuxName returns the deterministic honey_* tmux session name a
// redeemed access-request's guest shell runs in (handleJITRedeemTerminal),
// and that the operator's kill/watch routes target. It reuses
// ptyMuxSessionName's sanitizing/hashing so the result always satisfies
// validHoneyMuxSessionName before it ever reaches a tmux argv, independent of
// what characters the grant id happens to contain.
func shareGuestMuxName(grantID string) string {
	return ptyMuxSessionName(shareGuestSessionID(grantID))
}

// shareMuxAvailable reports whether this host can run a redeemed
// access-request's guest shell inside a tmux-backed multiplexer, making it
// observable (Part 2 of the share/watch feature). It is a package var so
// tests can force the plain, unobserved fallback without needing a real tmux
// server — production always checks for the tmux binary. Deliberately
// tmux-only (never zellij), mirroring ptyMuxBuildInterceptCommand: the
// watch/kill routes issue `tmux attach -r` / `tmux kill-session` directly, so
// a zellij-hosted guest shell could not be managed by them.
var shareMuxAvailable = func() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// ptyMuxBuildShareCommand builds the tmux attach-or-create command for a
// redeemed access-request's guest shell, mirroring ptyMuxBuildInterceptCommand:
// tmux-only, and the guest is attached with the ordinary exclusive
// (attach-or-create, read-write) behavior — it is the normal client of its
// OWN session, never a read-only one. muxName is always server-derived (see
// shareGuestMuxName), never a raw client-supplied id, but ptyMuxTmuxCommand
// re-validates it immediately before it reaches a tmux argv regardless, same
// invariant as every other mux path.
func ptyMuxBuildShareCommand(bin, configPath, encodedPayload, muxName string) (cmd *exec.Cmd, resolvedName string, useZellij bool, err error) {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil, muxName, false, fmt.Errorf("neither zellij nor tmux found on the server")
	}
	proxyArgs := ptyProxyExecArgs("pty-proxy", bin, configPath, encodedPayload)
	return ptyMuxTmuxCommand(muxName, proxyArgs)
}

// ptyMuxTmuxWatchAttach builds the OPERATOR's own read-only attach to a
// guest's already-running access-request session (Part 2 of the share/watch
// feature): `tmux attach -r -t <name>`. Unlike the guest's own exclusive
// attach, this NEVER creates or respawns a session — a watch request for a
// session that has already ended must fail, never conjure one masquerading as
// the one being watched. name is re-validated here, immediately before it
// reaches a tmux argv, independent of the caller (handleShareWatch derives it
// itself from the trusted grant, never from a raw client-supplied string, but
// the "#nosec G204 -- name sanitized" invariant holds at this call site too).
func ptyMuxTmuxWatchAttach(name string) (*exec.Cmd, error) {
	if !validHoneyMuxSessionName(name) {
		return nil, fmt.Errorf("invalid mux session name %q", name)
	}
	if !tmuxSessionAlive(name) {
		return nil, fmt.Errorf("share session %q has ended", name)
	}
	cmd := withBrowserTerm(exec.Command("tmux", "attach", "-r", "-t", name)) // #nosec G204 -- name sanitized
	zap.L().Debug("handleShareWatch: tmux watch attach", zap.String("session", name))
	return cmd, nil
}

// tmuxBoundedRunTimeout bounds every tmux call issued through tmuxRunGuest: a
// wedged or slow tmux server must not be able to hang the HTTP/WS request
// that triggered it (the share/sessions list, kill, and watch routes are all
// reachable from an authenticated but frequent or unauthenticated-adjacent
// path). It is a var, not a const, so a test can shrink it without a
// multi-second sleep.
var tmuxBoundedRunTimeout = 2 * time.Second

// tmuxRunGuest is the bounded exec seam for any tmux call reachable from a
// share-related web request (list/kill/watch a guest's access-request
// session): unlike the shared tmuxRun (intercept_mux.go — no timeout, used by
// cheap, synchronous, operator/system-triggered calls where a hang would
// itself be a loud bug worth surfacing), this ALWAYS bounds tmux with a
// context deadline. It is a package var so tests can fake it without a real
// tmux server or a real multi-second wait. exec.CommandContext kills the
// child the instant the deadline fires, so no tmux process is left to leak.
var tmuxRunGuest = func(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxBoundedRunTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput() // #nosec G204 -- fixed verbs; target args validated/generated, never raw guest bytes
	if err != nil && ctx.Err() != nil {
		return out, fmt.Errorf("tmux command timed out after %s: %w", tmuxBoundedRunTimeout, ctx.Err())
	}
	return out, err
}

// ptyProxyStdinPolicy controls how ptyProxyRunBridge handles inbound
// WebSocket stdin/control traffic. The zero value is the pre-existing
// operator/non-guest behavior — stdin forwarded straight to ptmx, resize
// honored — so every pre-existing caller passing the zero value keeps
// byte-identical behavior.
type ptyProxyStdinPolicy struct {
	// DropStdin discards every BinaryMessage frame instead of writing it
	// anywhere. Set for an OBSERVER — the operator's read-only watch of a
	// guest's access-request session (handleShareWatch): on top of tmux's own
	// `-r` attach (defense in depth, not the primary control), this means our
	// own code never even attempts a write for that connection.
	DropStdin bool
	// IgnoreResize (LOW-5) means this connection never drives window sizing at
	// all: neither a later "resize" control frame NOR the very first hello
	// cols/rows (round-2 residual — a guest's hello was still reaching
	// pty.Setsize even though resize FRAMES were already dropped; measured: a
	// 40x10 guest shrank a detached operator's 200x50 window to 40x9 and it
	// stayed). Set for an observer — the guest alone drives sizing, full stop.
	//
	// Round 2 also pinned tmux's window-size option to "manual" on guest
	// attach as a server-side belt to this brace. Round 3 (NEW-10) removed
	// that: it mutated the OPERATOR's session permanently (nothing ever
	// unpinned it), so any single share broke the operator's own browser
	// resize for the rest of that session's life. The default window-size
	// ("latest") already self-heals a guest-induced shrink the instant the
	// operator reattaches (measured on tmux 3.5a: 200x50 -> 80x23 -> 200x49),
	// so this hello-size skip alone is the fix that actually stuck.
	IgnoreResize bool
	// OperatorGuard (FIX-2) supplies the risk+policy inputs for the SAME
	// per-command guard, applied to the OPERATOR's own ptmx writes (the
	// default branch below) instead of a guest relay: web.guard_mode is a
	// config value that must actually gate the operator's normal browser
	// terminal, and for the common case (tmux/zellij present) that terminal
	// takes THIS mux path, not handleWebInteractiveStreams's stdinPipeR.
	// Mode comes straight from config, never forced — newGuardRelay's own
	// ModeOff fast path keeps the default (off) byte-identical to no wrap at
	// all. nil (every guest caller, and any caller predating this fix)
	// disables it entirely.
	OperatorGuard *termGuardInputs
	// KillOnCancel, when set, is force-killed the instant the bridge is
	// cancelled (either side closing), on top of the existing
	// ptmx.SetReadDeadline unblock below. That deadline trick only works when
	// ptmx is a "pollable" *os.File (Go's runtime poller can interrupt an
	// in-flight blocking Read); a REAL pty master from github.com/creack/pty
	// is not pollable on darwin, so a plain disconnect left the ptmx-reading
	// goroutine blocked forever, ptyProxyRunBridge never returned, and
	// ptyProxyTeardown (which closes ptmx and reaps the mux client) never ran
	// at all — measured as an observer's read-only tmux client staying
	// attached indefinitely after the operator closed the watch modal.
	// Killing this process instead closes ITS end of the pty, which delivers
	// EOF to our blocked master read on every platform, independent of
	// pollability. Set to the mux attach client's own *os.Process
	// (handleShareWatch) — never anything that could affect a different
	// client or the guest's session, since each bridge owns exactly one mux
	// client process.
	KillOnCancel *os.Process
	// SizeSync, when set, keeps an OBSERVER's pty matched to the mux window
	// it is attached to for the life of the bridge: polling
	// SizeSync.MuxName's current size through the bounded tmuxRunGuest
	// runner and, on change, both pty.Setsize-ing ptmx and sending a
	// {"size":{"cols":...,"rows":...}} control frame so the browser's
	// terminal can match it (see ShareWatchModal.tsx). The initial size sync
	// happens once in handleShareWatch before the bridge starts (so the very
	// first byte is already drawn at the right size); this only tracks
	// CHANGES for the rest of the connection (a guest resizing mid-session).
	// The poller shares wsOut (the same mutex the stdout pump writes
	// through) and bridgeCtx (so it always exits with the bridge, verified
	// by goleak) — never a second, unsynchronized conn.WriteMessage caller.
	SizeSync *ptyObserverSizeSync
}

// ptyObserverSizeSync is ptyProxyStdinPolicy.SizeSync's config: which mux to
// poll and how often. Interval<=0 uses shareWatchSizePollInterval.
// InitialCols/InitialRows should be whatever size the caller already sent the
// browser before the bridge started (handleShareWatch's own one-time query):
// seeding the poller's "last known" size with it means the poller's first
// tick only sends a NEW frame when the guest's window actually changed,
// instead of always resending a redundant duplicate of the size the browser
// already has.
type ptyObserverSizeSync struct {
	MuxName                  string
	Interval                 time.Duration
	InitialCols, InitialRows int
}

// shareWatchSizePollInterval is how often ptyProxyRunBridge's observer
// size-sync poller re-checks the guest's window size (WATCHFIT-1: a guest
// may resize mid-session). A package var, like tmuxBoundedRunTimeout, so a
// test can shrink it instead of waiting multiple seconds.
var shareWatchSizePollInterval = 2 * time.Second

// shareGuestWindowSize queries mux's current tmux window size through the
// package's bounded tmux runner (tmuxRunGuest): a wedged tmux server must
// not be able to hang this, since it is polled repeatedly for the life of an
// observer's connection. mux is re-validated here regardless of the caller,
// same invariant as every other tmux argv in this package.
func shareGuestWindowSize(mux string) (cols, rows int, ok bool) {
	if !validHoneyMuxSessionName(mux) {
		return 0, 0, false
	}
	out, err := tmuxRunGuest("display", "-p", "-t", mux, "#{window_width}x#{window_height}")
	if err != nil {
		return 0, 0, false
	}
	w, h, found := strings.Cut(strings.TrimSpace(string(out)), "x")
	if !found {
		return 0, 0, false
	}
	// ParseUint with a 16-bit size, not Atoi: a terminal dimension IS a uint16
	// (that is what pty.Winsize holds), so anything outside that range is a
	// nonsense reply and is rejected where it enters rather than silently
	// clamped further down in ptyWinsize. It also keeps the narrowing conversion
	// bounded by construction instead of by a later range check.
	cw, err1 := strconv.ParseUint(w, 10, 16)
	ch, err2 := strconv.ParseUint(h, 10, 16)
	if err1 != nil || err2 != nil || cw == 0 || ch == 0 {
		return 0, 0, false
	}
	return int(cw), int(ch), true
}

// shareWatchSizeFrame builds the {"size":{"cols":...,"rows":...}} control
// frame handleShareWatch/ptyProxyRunBridge send an observer so its viewer can
// match the guest's real window size instead of guessing (WATCHFIT-1). Plain
// ints need no JSON escaping, unlike the free-text error frames elsewhere in
// this file.
func shareWatchSizeFrame(cols, rows int) string {
	return fmt.Sprintf(`{"size":{"cols":%d,"rows":%d}}`, cols, rows)
}

// ptyProxyPollObserverSize is ptyProxyStdinPolicy.SizeSync's poller: it never
// issues anything but the read-only shareGuestWindowSize query above, so it
// can never itself be the thing that resizes the guest's window — only ever
// pty.Setsize on the OBSERVER's own ptmx (which tmux's `-r`/ignore-size
// attach already keeps from feeding back into the shared window, per
// ptyProxyStdinPolicy.IgnoreResize's doc) plus a control frame the browser
// consumes. Exits the instant ctx is done; never blocks the caller past
// that.
func ptyProxyPollObserverSize(ctx context.Context, ptmx *os.File, wsOut *wsWriter, sync ptyObserverSizeSync) {
	interval := sync.Interval
	if interval <= 0 {
		interval = shareWatchSizePollInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	lastCols, lastRows := sync.InitialCols, sync.InitialRows
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cols, rows, ok := shareGuestWindowSize(sync.MuxName)
			if !ok || (cols == lastCols && rows == lastRows) {
				continue
			}
			lastCols, lastRows = cols, rows
			ws := ptyWinsize(cols, rows)
			if err := pty.Setsize(ptmx, &ws); err != nil {
				zap.L().Warn("share watch: failed to resize observer pty", zap.Error(err))
				continue
			}
			_ = wsOut.writeText(shareWatchSizeFrame(cols, rows))
		}
	}
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
	// The two cancellation watchers below block until bridgeCtx is done, so they
	// CANNOT join wg: wg.Wait() runs before this function's deferred
	// bridgeCancel, and waiting on them there would deadlock. They get their own
	// group, waited after an explicit cancel, so this function only returns once
	// every goroutine it started has exited (otherwise a caller — and goleak —
	// observes them still running).
	var watchWg sync.WaitGroup
	watchWg.Add(2)

	ptyExited := make(chan struct{})

	// LOW-7: a plain disconnect (browser tab closed, network dropped) makes
	// the conn-reading goroutine below return and cancel bridgeCtx, but the
	// ptmx-reading goroutine only checks bridgeCtx between reads — if it is
	// currently blocked in ptmx.Read on an idle session (nothing to read),
	// that check never runs again until some other byte eventually arrives,
	// leaving a guest's own tmux client attached to the OPERATOR's session
	// indefinitely. This watcher force-expires that blocked Read the instant
	// the bridge is cancelled, so the guest's client detaches promptly.
	//
	// KillOnCancel (WATCHFIT-2) is the belt to this brace: SetReadDeadline
	// only unblocks a pending Read when ptmx is a "pollable" *os.File, which
	// a real creack/pty master is NOT on darwin (measured — see the field's
	// doc) — on that platform the deadline call above is a silent no-op
	// against an in-flight blocking read, so ptyProxyRunBridge never returns
	// and ptyProxyTeardown's ptmx.Close()+reap never runs at all. Killing the
	// mux client process closes ITS end of the pty, delivering EOF to our
	// blocked master read regardless of pollability.
	go func() {
		defer watchWg.Done()
		<-bridgeCtx.Done()
		_ = ptmx.SetReadDeadline(time.Now())
		if stdin.KillOnCancel != nil {
			_ = stdin.KillOnCancel.Kill()
		}
	}()

	// NEW-12 residual (round 4): the symmetric watcher for the OTHER
	// direction. conn.ReadMessage() below has no deadline and no select on
	// bridgeCtx.Done() — so when the guest's connection is dead in BOTH
	// directions (network partition, frozen tab: exactly the case NEW-12
	// named, where TCP keepalive doesn't help) and the operator's pane is
	// still producing output, the write side now correctly times out
	// (wsWriter's write deadline) and calls bridgeCancel(), but the reader
	// stays blocked in ReadMessage forever — wg.Wait() never returns,
	// ptyProxyTeardown never runs, and the guest's tmux client stays
	// attached to the operator's session indefinitely. Closing conn here
	// unblocks it, same pattern as ws_intercept.go's pump teardown. Safe
	// against the caller's own deferred conn.Close(): gorilla's Close is
	// idempotent, and nothing else here assumes single ownership of conn.
	go func() {
		defer watchWg.Done()
		<-bridgeCtx.Done()
		_ = conn.Close()
	}()

	// WATCHFIT-1: an observer's size-sync poller, tied to the SAME bridgeCtx
	// as everything else here so it always exits with the bridge (verified by
	// goleak) — never a goroutine that outlives this function. Shares wsOut
	// (not a second, unsynchronized conn.WriteMessage caller) since the
	// stdout pump below writes through it too.
	if stdin.SizeSync != nil {
		watchWg.Add(1)
		go func() {
			defer watchWg.Done()
			ptyProxyPollObserverSize(bridgeCtx, ptmx, wsOut, *stdin.SizeSync)
		}()
	}

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

	// The per-command guard (see OperatorGuard's doc) lives for the LIFE of
	// this bridge: a command line reconstructed by termguard can straddle two
	// WS frames. Built here (not by the caller) so its block/warn notices
	// share wsOut's write mutex with the stdout pump above. nil (every
	// pre-existing caller) leaves operatorGuardRelay nil, so that branch stays
	// byte-identical to pre-fix behavior.
	var operatorGuardRelay func([]byte) []byte
	if stdin.OperatorGuard != nil {
		// The operator's own terminal: notices go straight into wsOut, same
		// as termguard does for the SSH gateway's peer — this IS the
		// operator's own pane, so no desync risk.
		decide, onDecision := newTermGuardDecide(*stdin.OperatorGuard)
		operatorGuardRelay, _ = newGuardRelay(bridgeCtx, wsOut, stdin.OperatorGuard.Mode, decide, onDecision)
	}

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
					// An observer (e.g. the operator's read-only watch of a
					// guest session): never deliver its bytes anywhere.
					continue
				default:
					// FIX-2: gate the operator's own ptmx writes too — the
					// common case for a normal browser terminal (tmux/zellij
					// present), which web.guard_mode must actually reach.
					// nil operatorGuardRelay (every pre-existing caller)
					// keeps this branch untouched.
					if operatorGuardRelay != nil {
						payload = operatorGuardRelay(payload)
					}
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
	// Cancel here rather than leaving it to the deferred call, so the two
	// watchers unblock and can be waited for before this function returns.
	// bridgeCancel is idempotent, so the defer above stays harmless.
	bridgeCancel()
	watchWg.Wait()
	_ = ptmx.SetReadDeadline(time.Now())
	return ptyExited
}

// ptyProxyHandleCtrl handles one JSON control frame. ignoreResize drops a
// "resize" frame outright (LOW-5, observer paths): detach/close_tab are
// always honored, since neither lets an observer touch the session it merely
// attached to.
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
// true for a bridge that is NOT the exclusive owner of its session — today
// that is only the operator's read-only watch attach (handleShareWatch;
// LOW-6's original guest-attach case no longer exists, but the invariant it
// established still applies to this one): it skips ptyMuxKillSessionIfExited
// even on a natural ptyExited, so this bridge can NEVER be the one to reap
// the session it merely attached to — not through the explicit close_tab (×)
// branch (already a no-op killSession there) and not through this "all panes
// exited" cleanup either. The invariant is absolute, not scoped to one
// teardown branch.
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

// guard (FIX-2) carries the operator's per-command guard inputs (web.guard_mode);
// see ptyProxyStdinPolicy.OperatorGuard.
func handleWebPtyProxy(conn *websocket.Conn, helloRaw []byte, hello WSHello, recorder *engine.SessionRecorder, configPath string, guard termGuardInputs) error {
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
	ptyExited := ptyProxyRunBridge(ptmx, conn, recorder, hello, muxName, closeTabKill, ptyProxyStdinPolicy{OperatorGuard: &guard})
	ptyProxyTeardown(ptmx, cmd, muxName, useZellij, closeTabKill, ptyExited, func() { ptyMuxKillSession(muxName, useZellij) }, false)
	return nil
}

// guestReadLimitBytes bounds a single WebSocket message from a share-link
// guest (NEW-5, round 2): with no limit, an unauthenticated share-code
// holder could send one giant frame that honey-web fully buffers in its own
// heap. 64KiB comfortably covers a real paste while keeping a worst-case
// frame small.
//
// NEW-11 (round 3): set via conn.SetReadLimit in jit_redeem_ws.go
// immediately after the WebSocket upgrade, before the very first read (the
// hello frame), so every redeem branch is covered.
const guestReadLimitBytes = 64 * 1024

// handleShareGuestPtyProxy runs a redeemed access-request's guest shell
// inside a tmux-backed multiplexer under muxName (shareGuestMuxName(grant)),
// so the operator can later watch/kill it (Part 2 of the share/watch
// feature). The guest is the ordinary read-write client of its OWN session —
// this reuses ptyMuxTmuxCommand's exclusive attach-or-create behavior
// unchanged, never a read-only or relayed attach. Mirrors handleWebPtyProxy
// (the operator's own mux path) but is tmux-only (see
// ptyMuxBuildShareCommand) and keys off the grant-derived name instead of a
// client-supplied session id.
func handleShareGuestPtyProxy(conn *websocket.Conn, hello WSHello, recorder *engine.SessionRecorder, configPath string, guard termGuardInputs, muxName string) error {
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable: %w", err)
	}

	helloRaw, err := json.Marshal(hello)
	if err != nil {
		return fmt.Errorf("encode hello: %w", err)
	}
	encodedPayload := base64.StdEncoding.EncodeToString(helloRaw)
	cmd, resolvedName, useZellij, err := ptyMuxBuildShareCommand(bin, configPath, encodedPayload, muxName)
	if err != nil {
		return err
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("failed to start pty: %w", err)
	}

	closeTabKill := make(chan struct{}, 1)
	ptyExited := ptyProxyRunBridge(ptmx, conn, recorder, hello, resolvedName, closeTabKill, ptyProxyStdinPolicy{OperatorGuard: &guard})
	ptyProxyTeardown(ptmx, cmd, resolvedName, useZellij, closeTabKill, ptyExited, func() { ptyMuxKillSession(resolvedName, useZellij) }, false)
	return nil
}
