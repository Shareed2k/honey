// Package termguard implements a best-effort, per-command interactive
// guardrail shared by honey's SSH gateway and its web/share terminals: it
// reconstructs the client's current input line from raw keystrokes and
// consults a caller-supplied policy decision on Enter. See Reader for the
// important caveats — this is a speed-bump, not a security boundary.
package termguard

import (
	"context"
	"io"
	"strings"
)

// Mode selects the per-command interactive guardrail behavior. See NewReader
// for the important best-effort caveats.
type Mode string

const (
	// ModeOff disables interception entirely (NewReader returns the inner
	// reader unchanged: zero overhead).
	ModeOff Mode = "off"
	// ModeAudit runs the same reconstruction and decision as ModeEnforce but
	// never blocks: every line's verdict is recorded via onDecision.
	ModeAudit Mode = "audit"
	// ModeEnforce discards a denied line before it reaches the target (its
	// Enter is replaced with a Ctrl-U) and writes a policy notice to notify.
	ModeEnforce Mode = "enforce"
)

// ParseMode maps a config string to a Mode, defaulting to ModeOff for the
// empty string or any unrecognized value (fail safe: no interception).
func ParseMode(s string) Mode {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeAudit:
		return ModeAudit
	case ModeEnforce:
		return ModeEnforce
	default:
		return ModeOff
	}
}

// readChunk is how many bytes are read from src per Read. Output is always
// the same length as the chunk consumed (each keystroke maps to exactly one
// forwarded byte), so a chunk larger than the caller's buffer is delivered
// across several Reads via the carry (out).
const readChunk = 4096

// lineMax bounds the reconstructed input line so a client that never sends a
// newline cannot grow the buffer without limit. Bytes past the cap are still
// forwarded to the target; they are only dropped from the reconstruction.
const lineMax = 4096

// Control bytes handled while reconstructing the current input line.
const (
	ctrlC   = 0x03 // Ctrl-C: abort the current line
	ctrlU   = 0x15 // Ctrl-U: kill (discard) the current line
	ctrlH   = 0x08 // Ctrl-H / backspace
	ctrlDEL = 0x7f // DEL: backspace on most terminals
	enter1  = '\r'
	enter2  = '\n'
)

// reader is an io.Reader on the interactive stdin path (client keystrokes →
// target). It forwards every byte immediately so the target keeps echoing
// (interactivity is preserved) while reconstructing the current input line as
// bytes flow. On Enter it evaluates the reconstructed command through the SAME
// risk+policy gate exec uses:
//
//   - ModeAudit:   the Enter is always forwarded (the command runs); the
//     decision is recorded via onDecision.
//   - ModeEnforce: a denied command's Enter is replaced with a Ctrl-U
//     (kill-line) so the pending line is discarded on the target and never
//     executed, a one-line policy notice is written to notify, and the deny is
//     recorded. Allowed commands forward the Enter unchanged.
//   - ModeOff:     NewReader returns src unchanged (no reconstruction, no
//     overhead).
//
// BEST-EFFORT ONLY. A PTY does its own line editing: readline history, arrow
// keys and other escape sequences, and bracketed paste can all desync this
// reconstruction from what the target's shell actually parses. Enforce mode is
// a speed-bump, not a security boundary — the target-side command-risk gate
// (cmdgate, applied by the record's own honey backend) remains authoritative.
// There are no goroutines and the line buffer is bounded by lineMax.
type reader struct {
	ctx        context.Context
	src        io.Reader
	notify     io.Writer
	mode       Mode
	decide     func(ctx context.Context, command string) (reason string, denied bool)
	onDecision func(command, reason string, denied bool)

	line []byte // reconstructed current input line (bounded by lineMax)

	// readBuf is the reusable source buffer; out is the carry of already
	// processed bytes that did not fit the caller's p. src is never read again
	// until out is fully drained, so out may alias readBuf safely. pendErr holds
	// a src read error until the carry it accompanied has been delivered.
	readBuf []byte
	out     []byte
	pendErr error
}

// NewReader wraps inner, an interactive stdin path, reconstructing each
// command line and consulting decide on Enter. decide returns (denyReason,
// denied). onDecision is called for every completed line (allow or deny) for
// audit. When mode is ModeOff it returns inner unchanged so the interactive
// path stays a transparent, zero-overhead pass-through.
func NewReader(
	ctx context.Context,
	inner io.Reader,
	notify io.Writer,
	mode Mode,
	decide func(ctx context.Context, command string) (reason string, denied bool),
	onDecision func(command, reason string, denied bool),
) io.Reader {
	if mode == ModeOff {
		return inner
	}
	return &reader{
		ctx:        ctx,
		src:        inner,
		notify:     notify,
		mode:       mode,
		decide:     decide,
		onDecision: onDecision,
	}
}

// Read forwards keystrokes to the target, transforming them per the guard mode.
func (m *reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	// Deliver any carried output before reading more. src is not read again
	// until the carry is fully drained, so readBuf stays valid underneath it.
	if len(m.out) > 0 {
		n := copy(p, m.out)
		m.out = m.out[n:]
		if len(m.out) == 0 {
			err := m.pendErr
			m.pendErr = nil
			return n, err
		}
		return n, nil
	}
	if m.pendErr != nil {
		err := m.pendErr
		m.pendErr = nil
		return 0, err
	}

	if m.readBuf == nil {
		m.readBuf = make([]byte, readChunk)
	}
	n, err := m.src.Read(m.readBuf)
	if n == 0 {
		return 0, err
	}
	m.process(m.readBuf[:n])

	c := copy(p, m.readBuf[:n])
	if c < n {
		m.out = m.readBuf[c:n]
		m.pendErr = err
		return c, nil
	}
	return c, err
}

// process transforms one chunk of client keystrokes in place (each input byte
// maps to exactly one output byte, so the chunk length never changes) while
// reconstructing the current input line and gating whole commands on Enter.
func (m *reader) process(chunk []byte) {
	for i := range chunk {
		switch b := chunk[i]; b {
		case enter1, enter2:
			cmd := strings.TrimSpace(string(m.line))
			if cmd != "" {
				reason, denied := m.decide(m.ctx, cmd)
				m.onDecision(cmd, reason, denied)
				if m.mode == ModeEnforce && denied {
					// Replace Enter with Ctrl-U so the target discards the
					// pending line instead of executing it.
					chunk[i] = ctrlU
					m.notifyBlocked(reason)
				}
			}
			m.line = m.line[:0]
		case ctrlDEL, ctrlH:
			if len(m.line) > 0 {
				m.line = m.line[:len(m.line)-1]
			}
		case ctrlU, ctrlC:
			m.line = m.line[:0]
		default:
			// Drop from reconstruction past the cap, but still forward the byte.
			if len(m.line) < lineMax {
				m.line = append(m.line, b)
			}
		}
	}
}

// notifyBlocked writes a one-line policy notice to the client (best-effort; any
// write error is ignored — the deny is still enforced and audited).
func (m *reader) notifyBlocked(reason string) {
	if m.notify == nil {
		return
	}
	_, _ = io.WriteString(m.notify, "\r\n\x1b[31m[blocked by policy: "+reason+"]\x1b[0m\r\n")
}

// Snapshot returns a copy of the reconstructed line's current state, for a
// caller to later Restore if bytes it fed to Read via a message-oriented
// relay (rather than a continuous stream) ultimately failed to reach the
// target — see Restore.
func (m *reader) Snapshot() []byte {
	return append([]byte(nil), m.line...)
}

// Restore rolls the reconstructed line back to a snapshot taken by Snapshot,
// undoing everything process() did to it since — appended characters, and a
// completed line's Enter, which unconditionally clears the line whether it
// was forwarded or replaced with a Ctrl-U. Exported so a caller that drives
// Read from discrete, already-delivered messages — e.g. a relay that hands
// one WebSocket frame at a time to Read — can undo a frame's effect on the
// reconstruction when that frame's bytes ultimately never reached the target
// (a downstream size cap, a transport error).
//
// Restore, not a blind clear, is required: the target's own input-line
// buffer didn't change either when a frame failed to arrive, so the guard's
// reconstruction must roll back to match it exactly. A blind clear desyncs
// the guard from the target in BOTH directions — it can forget a still-live
// dangerous line that never actually got the deny it needed (the substituted
// Ctrl-U never arrived either, so the target's real buffer is untouched),
// or drop a line the target still has pending, letting a later bare Enter
// run it with no decide call and no audit trail at all. A plain io.Reader
// consumer never needs this.
func (m *reader) Restore(snapshot []byte) {
	m.line = append(m.line[:0], snapshot...)
}
