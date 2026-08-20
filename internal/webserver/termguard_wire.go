package webserver

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/guardrails"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/termguard"
)

// termGuardInputs carries the risk+policy inputs for the per-command web/
// share terminal guard (internal/termguard) — the SAME shape whether the
// caller is a normal operator terminal (Mode from web.guard_mode, off by
// default) or a share-link collaborate guest (Mode always
// termguard.ModeEnforce, set by the caller — an untrusted party never gets a
// weaker mode via config).
type termGuardInputs struct {
	Enforcer   *policy.Enforcer
	Guardrails *guardrails.Ruleset
	Actor      string
	Record     hosts.Record
	AuditSink  audit.Sink
	Mode       termguard.Mode
}

// webGuardMode resolves the configured interactive guardrail mode for a
// normal operator web terminal (off/audit/enforce), defaulting to off for
// empty or unknown values. Mirrors sshgateway.Server.guardModeVal.
func (s *Server) webGuardMode() termguard.Mode {
	return termguard.ParseMode(s.opts.GuardMode)
}

// newTermGuardDecide builds the decide/onDecision pair termguard.NewReader
// needs, mirroring internal/sshgateway/session.go's interactive decide
// closure so one behavior serves the gateway and the web/share terminal: the
// SAME risk+policy assessment (cmdgate.AssessTargets) runExec and the
// gateway's own interactive guard use, denying on a hard verdict and writing
// (plus auditing) any non-fatal guardrail warning to notify. notify must be
// the connection's own write-serializing wsWriter (never a second, unrelated
// writer over the same *websocket.Conn) since it is invoked concurrently
// with the stdout pump.
//
// A policy-evaluation error fails closed only when in.Mode is
// termguard.ModeEnforce — audit mode records but never blocks, matching the
// gateway's behavior. A share collaborate guest always passes Mode enforce,
// so it always fails closed; a config-off/audit operator terminal does not.
func newTermGuardDecide(notify io.Writer, in termGuardInputs) (
	decide func(context.Context, string) (string, bool),
	onDecision func(cmd, reason string, denied bool),
) {
	risk := func(cmd string) string { return string(commandrisk.AnalyzeStep(cmd, "sh").MaxSeverity) }
	logAudit := func(cmd, decision, reason string) {
		if in.AuditSink == nil {
			return
		}
		_ = in.AuditSink.Log(context.Background(), audit.Event{
			Source:     "web",
			Actor:      in.Actor,
			Action:     "interactive_command",
			Target:     in.Record.Name,
			Command:    cmd,
			Risk:       risk(cmd),
			Decision:   decision,
			DenyReason: reason,
		})
	}

	// policyErrDetail carries the full policy-evaluation error from decide to
	// onDecision for ONE line (FIX-5): decide must return a generic,
	// client-safe reason (a share guest is untrusted, unlike the SSH
	// gateway's authenticated operator peer), but the audit event should
	// still record the real error for an investigator. termguard.Reader.
	// process calls decide then onDecision synchronously for the same line
	// (never concurrently, never interleaved with another line), so a single
	// variable shared by the two closures is safe without a lock.
	var policyErrDetail string

	decide = func(ctx context.Context, cmd string) (string, bool) {
		policyErrDetail = ""
		_, decisions, derr := cmdgate.AssessTargets(ctx, in.Enforcer, in.Guardrails, cmd, "sh",
			[]cmdgate.TargetInput{{Name: in.Record.Name, PolicyInput: cmdgate.CommandPolicyInput(in.Actor, in.Record, cmd), Attrs: cmdgate.RecordAttrs(in.Record)}}, false)
		if derr != nil {
			policyErrDetail = "policy error: " + derr.Error()
			return "blocked by policy", in.Mode == termguard.ModeEnforce
		}
		if len(decisions) == 0 {
			return "", false
		}
		if decisions[0].Denied {
			return decisions[0].Reason, true
		}
		// Guardrail warn (not denied): surface a yellow notice and audit each
		// rule message, same as the gateway's interactive decide closure.
		for _, w := range decisions[0].Warnings {
			_, _ = io.WriteString(notify, "\r\n\x1b[33m[guardrail: "+w+"]\x1b[0m\r\n")
			logAudit(cmd, "warn", w)
		}
		return "", false
	}
	onDecision = func(cmd, reason string, denied bool) {
		decision := "allow"
		if denied {
			decision = "deny"
		}
		// A policy-evaluation error must be audited with its real detail
		// regardless of whether it ended up denied (enforce) or merely
		// allowed through (audit/off) — otherwise an OPA failure in audit
		// mode is recorded as a bare "allow" with no reason at all.
		switch {
		case policyErrDetail != "":
			reason = policyErrDetail
		case !denied:
			reason = ""
		}
		logAudit(cmd, decision, reason)
	}
	return decide, onDecision
}

// ansiSGR matches the SGR (color) escape sequences termguard and
// newTermGuardDecide use to color their policy notices for a peer that owns
// the terminal they land in (an operator, or the SSH gateway's authenticated
// peer) — see guestNoticeWriter.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// guestNoticeWriter adapts termguard's raw, ANSI-colored policy notices to a
// collaborate guest's distinct {"notice":...} text-frame lane (FIX-4). A
// guest's terminal MIRRORS the OPERATOR's pane, so writing policy text
// straight into its buffer — as termguard normally does for a peer that owns
// its own session (the SSH gateway's peer, or the operator's own web
// terminal) — would desync that mirror until the next redraw; round 3
// already moved the relay drop notice out of the terminal buffer for
// exactly this reason (see tmuxSendKeysHex's caller). Shares wsOut's write
// mutex (never a second, independent writer over the same *websocket.Conn).
type guestNoticeWriter struct{ wsOut *wsWriter }

func (g guestNoticeWriter) Write(p []byte) (int, error) {
	if msg := strings.TrimSpace(ansiSGR.ReplaceAllString(string(p), "")); msg != "" {
		// json.Marshal (not manual string concatenation) so a deny reason
		// carrying any control byte other than \ " \n \r — which escapeJSON
		// does not handle — never produces a frame the client's JSON.parse
		// throws on, silently losing the notice.
		if b, err := json.Marshal(noticeFrame{Notice: msg}); err == nil {
			_ = g.wsOut.writeText(string(b))
		}
	}
	return len(p), nil
}

// noticeFrame is the {"notice":...} text-frame shape guest notices use — see
// guestNoticeWriter and the NEW-17 drop notice in ptyProxyRunBridge.
type noticeFrame struct {
	Notice string `json:"notice"`
}

// relayChunkReader drives a termguard.Reader synchronously, one already-read
// WS frame at a time — see newGuardRelay.
type relayChunkReader struct{ chunk []byte }

func (r *relayChunkReader) Read(p []byte) (int, error) {
	if len(r.chunk) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.chunk)
	r.chunk = r.chunk[n:]
	return n, nil
}

// lineSnapshotter is optionally implemented by the io.Reader
// termguard.NewReader returns (never for ModeOff, which returns the inner
// reader unchanged) so a message-oriented caller can undo a frame's effect
// on the in-progress reconstructed line — see newGuardRelay's rollback.
type lineSnapshotter interface {
	Snapshot() []byte
	Restore([]byte)
}

// newGuardRelay adapts termguard.NewReader — an io.Reader built for a
// continuous stdin stream — to a relay's message-oriented shape: a caller
// that already has one WS frame at a time in hand, with no io.Reader
// upstream of it to plug termguard into directly (the collaborate-guest
// relay, and the operator mux path's ptmx writes — see FIX-2). mode comes
// straight from the caller (never re-decided here), so it is the single
// source of truth for both the block/allow behavior AND the fail-closed
// check in newTermGuardDecide.
//
// relay gates one frame per call and is driven from the SAME goroutine that
// read the frame — no io.Pipe, no extra goroutine. relayChunkReader hands
// termguard exactly the bytes of the current frame; termguard's own internal
// buffer (4096 bytes) can be smaller than a frame, so the loop below may call
// Read more than once to drain it completely — process() maps each input
// byte to exactly one output byte, so total output always equals total
// input, which is what bounds the loop.
//
// rollback undoes the LAST relay call's effect on the guard's in-progress
// reconstructed line, restoring exactly the snapshot taken right before that
// call. Call it when the bytes relay just processed ultimately FAILED to
// reach the target (FIX-1): the target's own input-line buffer didn't
// change either, so the guard must roll back to match it — never a blind
// clear, which desyncs the guard from the target in both directions (see
// termguard.Reader.Restore's doc for the full failure mode: a still-live
// dangerous line forgotten with no deny ever delivered, or a denied line's
// Enter-triggered clear left standing even though neither the original text
// nor the substituted Ctrl-U ever arrived). The frame-cap check itself must
// still run BEFORE relay is even called on a per-connection byte budget
// (see ptyProxyRunBridge) — rollback only covers a frame that passed the cap
// but still failed to relay for some other reason (a transport/exec error).
//
// mode == termguard.ModeOff (the operator's off default) makes both relay
// and rollback no-ops — termguard.NewReader returns the inner reader
// unchanged, so this never touches the guard machinery at all, byte-identical
// to no wrap. One instance is built per connection (like terminalReportFilter)
// so a command line split across frames still reconstructs correctly.
func newGuardRelay(ctx context.Context, notify io.Writer, mode termguard.Mode,
	decide func(context.Context, string) (string, bool),
	onDecision func(cmd, reason string, denied bool),
) (relay func([]byte) []byte, rollback func()) {
	if mode == termguard.ModeOff {
		return func(chunk []byte) []byte { return chunk }, func() {}
	}
	feeder := &relayChunkReader{}
	guarded := termguard.NewReader(ctx, feeder, notify, mode, decide, onDecision)
	snapshotter, _ := guarded.(lineSnapshotter)
	var lastSnapshot []byte
	relay = func(chunk []byte) []byte {
		if len(chunk) == 0 {
			return chunk
		}
		if snapshotter != nil {
			lastSnapshot = snapshotter.Snapshot()
		}
		feeder.chunk = chunk
		out := make([]byte, 0, len(chunk))
		buf := make([]byte, len(chunk))
		for len(out) < len(chunk) {
			n, err := guarded.Read(buf)
			if n > 0 {
				out = append(out, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		return out
	}
	rollback = func() {
		if snapshotter != nil {
			snapshotter.Restore(lastSnapshot)
		}
	}
	return relay, rollback
}
