package webserver

import (
	"context"
	"io"

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

	decide = func(ctx context.Context, cmd string) (string, bool) {
		_, decisions, derr := cmdgate.AssessTargets(ctx, in.Enforcer, in.Guardrails, cmd, "sh",
			[]cmdgate.TargetInput{{Name: in.Record.Name, PolicyInput: cmdgate.CommandPolicyInput(in.Actor, in.Record, cmd), Attrs: cmdgate.RecordAttrs(in.Record)}}, false)
		if derr != nil {
			return "policy error: " + derr.Error(), in.Mode == termguard.ModeEnforce
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
		if !denied {
			reason = ""
		} else {
			decision = "deny"
		}
		logAudit(cmd, decision, reason)
	}
	return decide, onDecision
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

// newGuardRelay adapts termguard.NewReader — an io.Reader built for a
// continuous stdin stream — to the collaborate-guest relay's message-oriented
// shape: ptyProxyRunBridge's WS read pump already has one already-read frame
// at a time, with no io.Reader anywhere upstream of it to plug termguard into
// directly. The returned func gates one frame per call and is driven from the
// SAME goroutine that read the frame — no io.Pipe, no extra goroutine.
// relayChunkReader hands termguard exactly the bytes of the current frame;
// termguard's own internal buffer (4096 bytes) can be smaller than a frame,
// so the loop below may call Read more than once to drain it completely —
// process() maps each input byte to exactly one output byte, so total output
// always equals total input, which is what bounds the loop.
//
// One instance is built per connection (like terminalReportFilter) so a
// command line split across frames still reconstructs correctly.
func newGuardRelay(ctx context.Context, notify io.Writer, mode termguard.Mode,
	decide func(context.Context, string) (string, bool),
	onDecision func(cmd, reason string, denied bool),
) func([]byte) []byte {
	feeder := &relayChunkReader{}
	guarded := termguard.NewReader(ctx, feeder, notify, mode, decide, onDecision)
	return func(chunk []byte) []byte {
		if len(chunk) == 0 {
			return chunk
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
}
