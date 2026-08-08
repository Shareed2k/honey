package sshgateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/pvelxc"
	"github.com/shareed2k/honey/internal/truenasshell"
	"github.com/shareed2k/honey/internal/ui"
)

// sshStreamer is the gateway's SSH interactive fallback: it dials the record's
// leaf SSH directly via ui.RunSSHInteractiveStreams, independent of the exec
// registry. It mirrors the web server's sshFallbackStreamer so a nil
// ExecRegistry (or an executor that cannot stream a TTY) preserves the
// pre-Phase-F SSH-only interactive path exactly.
type sshStreamer struct{}

func (sshStreamer) RunInteractiveStreams(ctx context.Context, user string, r hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	return ui.RunSSHInteractiveStreams(ctx, user, r, stdin, stdout, cols, rows, resize)
}

var _ hostexec.InteractiveStreamer = sshStreamer{}

// isNativeTarget reports whether rec should be reached through the provider seam
// rather than a raw SSH leaf: a mesh-proxied record (the executor forwards it
// elsewhere), a docker container, or a k8s pod. Everything else (plain SSH) uses
// the leaf path.
func isNativeTarget(rec hosts.Record, ex hostexec.Executor) bool {
	return hostexec.IsProxy(ex) || rec.IsDocker() ||
		(rec.Provider == "k8s" && strings.EqualFold(rec.Meta["kind"], "pod"))
}

// resizeBuffer bounds the window-change queue so a flood of resizes cannot grow
// unbounded; excess events are dropped (the latest size still wins on the next
// forward).
const resizeBuffer = 16

// RFC 4254 session-request payloads, decoded with ssh.Unmarshal (field order,
// not names, is what matters).
type (
	ptyRequestPayload struct {
		Term     string
		Columns  uint32
		Rows     uint32
		WidthPx  uint32
		HeightPx uint32
		Modes    string
	}
	windowChangePayload struct {
		Columns  uint32
		Rows     uint32
		WidthPx  uint32
		HeightPx uint32
	}
	execPayload struct{ Command string }

	exitStatusPayload struct{ Status uint32 }
)

// startAction is the first shell/exec request on a session channel; it triggers
// the actual proxying once the client stops configuring the channel.
type startAction struct {
	isExec  bool
	command string
	wantPTY bool
	term    string
	cols    int
	rows    int
}

// serveSession accepts a session channel, waits for the shell/exec that starts
// the session, dispatches it, then always sends exit-status and closes.
func (s *Server) serveSession(ctx context.Context, newCh ssh.NewChannel, actor string) {
	ch, reqs, err := newCh.Accept()
	if err != nil {
		return
	}

	start := make(chan startAction, 1)
	resize := make(chan [2]int, resizeBuffer)
	go readSessionRequests(reqs, start, resize)

	var act startAction
	select {
	case <-ctx.Done():
		_ = ch.Close()
		return
	case a, ok := <-start:
		if !ok {
			// Client closed the channel without starting a shell/exec.
			_ = ch.Close()
			return
		}
		act = a
	}

	exit := s.dispatch(ctx, ch, actor, act, resize)
	sendExitStatus(ch, exit)
	_ = ch.Close()
}

// readSessionRequests drives the channel request state machine. It replies to
// every request, records pty/window state, and sends at most one startAction on
// the first shell/exec. window-change events are forwarded (non-blocking) to
// resize throughout the session. Both channels are closed on return so readers
// unwind cleanly.
func readSessionRequests(reqs <-chan *ssh.Request, start chan<- startAction, resize chan<- [2]int) {
	defer close(resize)
	defer close(start)

	var (
		wantPTY bool
		term    string
		cols    int
		rows    int
		started bool
	)
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var p ptyRequestPayload
			if err := ssh.Unmarshal(req.Payload, &p); err == nil {
				wantPTY = true
				term = p.Term
				cols = int(p.Columns)
				rows = int(p.Rows)
				reply(req, true)
			} else {
				reply(req, false)
			}
		case "window-change":
			var w windowChangePayload
			if err := ssh.Unmarshal(req.Payload, &w); err == nil {
				select {
				case resize <- [2]int{int(w.Columns), int(w.Rows)}:
				default:
				}
			}
			reply(req, true)
		case "env":
			reply(req, true)
		case "shell":
			if started {
				reply(req, false)
				continue
			}
			started = true
			reply(req, true)
			start <- startAction{wantPTY: wantPTY, term: term, cols: cols, rows: rows}
		case "exec":
			if started {
				reply(req, false)
				continue
			}
			var p execPayload
			if err := ssh.Unmarshal(req.Payload, &p); err != nil {
				reply(req, false)
				continue
			}
			started = true
			reply(req, true)
			start <- startAction{isExec: true, command: p.Command, wantPTY: wantPTY, term: term, cols: cols, rows: rows}
		default:
			reply(req, false)
		}
	}
}

// dispatch routes a started session: it resolves the resource (the first token
// of the ssh command) and either opens an interactive shell (resource only +
// pty) or runs an ad-hoc command (resource + trailing command).
func (s *Server) dispatch(ctx context.Context, ch ssh.Channel, actor string, act startAction, resize <-chan [2]int) int {
	stderr := ch.Stderr()
	if !act.isExec {
		fmt.Fprintln(stderr, "error: specify a resource, e.g. ssh <user>@gateway <resource> [command]")
		return 1
	}
	raw := strings.TrimSpace(act.command)
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		fmt.Fprintln(stderr, "error: empty command; specify a resource, e.g. ssh <user>@gateway <resource>")
		return 1
	}
	resource := fields[0]
	remainder := strings.TrimSpace(strings.TrimPrefix(raw, resource))

	rec, err := s.resolveResource(ctx, resource)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		s.audit(ctx, audit.Event{Actor: actor, Action: "resolve", Target: resource, Decision: "deny", DenyReason: err.Error()})
		return 1
	}

	if remainder == "" {
		if !act.wantPTY {
			fmt.Fprintln(stderr, "error: no command and no pty requested; add -t for an interactive shell")
			return 1
		}
		return s.runInteractive(ctx, ch, actor, rec, act.cols, act.rows, resize)
	}
	return s.runExec(ctx, ch, actor, rec, remainder, act)
}

// runInteractive gates the open (OPA interactive_session), records the session,
// and proxies an interactive PTY shell to the resolved target via the shared ui
// SSH streamer.
func (s *Server) runInteractive(ctx context.Context, ch ssh.Channel, actor string, rec hosts.Record, cols, rows int, resize <-chan [2]int) int {
	stderr := ch.Stderr()
	if err := s.gateInteractive(ctx, actor, rec); err != nil {
		fmt.Fprintf(stderr, "denied: %v\n", err)
		s.audit(ctx, audit.Event{Actor: actor, Action: "interactive_session", Target: rec.Name, Decision: "deny", DenyReason: err.Error()})
		return 1
	}

	recorder := s.newRecorder(rec, actor, "interactive")
	defer func() { _ = recorder.Close() }()

	s.audit(ctx, audit.Event{Actor: actor, Action: "interactive_session", Target: rec.Name, Decision: "allow"})

	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	recorder.RecordResize(cols, rows)

	stdin := engine.WrapRecordingReader(ch, recorder, "in")
	// Best-effort per-command interactive guardrail (defense-in-depth on top of
	// the authoritative target-side command-risk gate). decide runs the SAME
	// risk+policy assessment runExec uses against each reconstructed command
	// line; onDecision audits the verdict. When the mode is off, newGuardReader
	// returns stdin unchanged (zero overhead, zero behavior change). notify is
	// the raw client channel (ch) because a policy notice is honey's own text,
	// not target output, so it must bypass the masking writer; ssh.Channel
	// serializes concurrent writes, so writing it alongside the stdout pump is
	// safe.
	decide := func(gctx context.Context, cmd string) (string, bool) {
		_, decisions, derr := cmdgate.AssessTargets(gctx, s.opts.Enforcer, cmd, "sh",
			[]cmdgate.TargetInput{{Name: rec.Name, PolicyInput: commandPolicyInput(actor, rec, cmd)}}, false)
		if derr != nil {
			// Fail closed only in enforce mode; audit mode records but never blocks.
			return "policy error: " + derr.Error(), s.guardModeVal() == guardEnforce
		}
		if len(decisions) > 0 && decisions[0].Denied {
			return decisions[0].Reason, true
		}
		return "", false
	}
	onDecision := func(cmd, reason string, denied bool) {
		ev := audit.Event{
			Actor:    actor,
			Action:   "interactive_command",
			Target:   rec.Name,
			Command:  cmd,
			Risk:     string(commandrisk.AnalyzeStep(cmd, "sh").MaxSeverity),
			Decision: "allow",
		}
		if denied {
			ev.Decision = "deny"
			ev.DenyReason = reason
		}
		s.audit(ctx, ev)
	}
	stdin = newGuardReader(ctx, stdin, ch, s.guardModeVal(), decide, onDecision)
	// Mask outermost (closest to the target output) so the recorder and the
	// client both receive redacted bytes. Closed after the stream returns to
	// flush the retained tail.
	mw := NewMaskingWriter(engine.WrapRecordingWriter(ch, recorder, "out"), s.opts.MaskRules)
	targetUser := s.targetUser(rec, actor)

	// Forward resizes to the ui streamer while recording each one. The forwarder
	// stops the moment the streamer returns (stopFwd) so it never outlives the
	// session; readSessionRequests will also close resize on channel teardown.
	uiResize := make(chan [2]int, resizeBuffer)
	stopFwd := make(chan struct{})
	fwdDone := make(chan struct{})
	go func() {
		defer close(fwdDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopFwd:
				return
			case sz, ok := <-resize:
				if !ok {
					return
				}
				recorder.RecordResize(sz[0], sz[1])
				select {
				case uiResize <- sz:
				default:
				}
			}
		}
	}()

	// Console-only targets (Proxmox serial, TrueNAS shell) are reached over a
	// provider websocket rather than an SSH leaf or the exec seam; dispatch them
	// to the matching stream bridge, which reuses the same provider Session the web
	// terminal uses. The recorder+mask+guard wrappers on stdin/mw still apply, so
	// these sessions are recorded, masked, and guarded like every other target.
	var runErr error
	switch {
	case pvelxc.ShouldUsePVETTY(rec):
		runErr = s.runProxmoxConsole(ctx, rec, stdin, mw, cols, rows, uiResize)
	case truenasshell.ShouldUseTrueNASShell(rec, truenasshell.ConsoleTrueNASAPI):
		runErr = s.runTrueNASConsole(ctx, rec, stdin, mw, cols, rows, uiResize)
	default:
		// Route the shell through the provider seam when a registry is wired: an
		// executor that can stream a TTY (docker/k8s locally, or the honey proxy for
		// a mesh-routed record) serves it; otherwise fall back to the SSH leaf. The
		// recorder+mask+guard wrappers around stdin/mw are provider-agnostic, so
		// every target is recorded, masked, and guarded identically. A nil registry
		// keeps the pre-Phase-F SSH-only path (sshStreamer ==
		// ui.RunSSHInteractiveStreams).
		var is hostexec.InteractiveStreamer = sshStreamer{}
		if s.opts.ExecRegistry != nil {
			if r, ok := s.opts.ExecRegistry.ForRecord(rec).(hostexec.InteractiveStreamer); ok {
				is = r
			}
		}
		runErr = is.RunInteractiveStreams(ctx, targetUser, rec, stdin, mw, cols, rows, uiResize)
	}
	close(stopFwd)
	<-fwdDone
	_ = mw.Close()

	if runErr != nil {
		recorder.RecordError(runErr)
		s.audit(ctx, audit.Event{Actor: actor, Action: "interactive_exit", Target: rec.Name, Decision: "allow", DenyReason: runErr.Error()})
		return 1
	}
	s.audit(ctx, audit.Event{Actor: actor, Action: "interactive_exit", Target: rec.Name, Decision: "allow"})
	return 0
}

// runExec gates the command (command-risk + OPA command_exec), records it, then
// runs it non-interactively on the target, streaming stdin/stdout/stderr and
// returning the remote exit status.
func (s *Server) runExec(ctx context.Context, ch ssh.Channel, actor string, rec hosts.Record, command string, act startAction) int {
	stderr := ch.Stderr()

	// Console-only targets have no ad-hoc exec transport (they proxy an
	// interactive provider console, not a command channel); reject cleanly before
	// any gate or dial so the client gets an actionable message.
	if isConsoleTarget(rec) {
		fmt.Fprintf(stderr, "error: %s is a console-only target; use an interactive shell (ssh -t)\n", rec.Name)
		s.audit(ctx, audit.Event{Actor: actor, Action: "command_exec", Target: rec.Name, Command: command, Decision: "deny", DenyReason: "console-only target"})
		return 1
	}

	analysis, decisions, err := cmdgate.AssessTargets(ctx, s.opts.Enforcer, command, "sh",
		[]cmdgate.TargetInput{{Name: rec.Name, PolicyInput: commandPolicyInput(actor, rec, command)}}, false)
	if err != nil {
		fmt.Fprintf(stderr, "error: policy: %v\n", err)
		s.audit(ctx, audit.Event{Actor: actor, Action: "command_exec", Target: rec.Name, Command: command, Decision: "deny", DenyReason: err.Error()})
		return 1
	}
	risk := string(analysis.MaxSeverity)
	if len(decisions) > 0 && decisions[0].Denied {
		reason := decisions[0].Reason
		fmt.Fprintf(stderr, "denied: %s\n", reason)
		s.audit(ctx, audit.Event{Actor: actor, Action: "command_exec", Target: rec.Name, Command: command, Risk: risk, Decision: "deny", DenyReason: reason})
		return 1
	}
	s.audit(ctx, audit.Event{Actor: actor, Action: "command_exec", Target: rec.Name, Command: command, Risk: risk, Decision: "allow"})

	recorder := s.newRecorder(rec, actor, "exec")
	defer func() { _ = recorder.Close() }()

	// Transport selection: native providers (docker container, k8s pod) and
	// mesh-proxied records run through the hostexec seam; plain SSH targets keep
	// the raw-leaf path below (with the #169 PTY handling). A nil registry always
	// takes the SSH path. The gate + audit above already ran for both.
	var ex hostexec.Executor
	if s.opts.ExecRegistry != nil {
		ex = s.opts.ExecRegistry.ForRecord(rec)
	}
	if ex != nil && isNativeTarget(rec, ex) {
		// Native exec is non-tty: HostClient.RunWithStreams runs a one-shot command
		// on docker/k8s/mesh with no remote PTY (a client -t is honored only on the
		// SSH leaf path). Wire the same recorder+mask-wrapped streams so output is
		// recorded and redacted identically.
		hc, derr := ex.Dial(s.targetUser(rec, actor), rec)
		if derr != nil {
			fmt.Fprintf(stderr, "error: connect: %v\n", derr)
			recorder.RecordError(derr)
			return 1
		}
		defer func() { _ = hc.Close() }()

		nStdin := engine.WrapRecordingReader(ch, recorder, "in")
		nOut := NewMaskingWriter(engine.WrapRecordingWriter(ch, recorder, "out"), s.opts.MaskRules)
		nErr := NewMaskingWriter(engine.WrapRecordingWriter(stderr, recorder, "out"), s.opts.MaskRules)
		// Native exec has no remote tty, so its LF-only output would staircase in a
		// client that requested a PTY (ssh -t). Cook LF->CRLF for that case (the
		// masker/recorder sit under the cooker, so both the client and the recording
		// get the tty-equivalent output). Without -t the client is not in raw mode
		// and LF renders fine.
		var outW io.Writer = nOut
		var errW io.Writer = nErr
		if act.wantPTY {
			outW = newCRLFWriter(nOut)
			errW = newCRLFWriter(nErr)
		}
		runErr := hc.RunWithStreams(command, nStdin, outW, errW)
		_ = nOut.Close()
		_ = nErr.Close()
		exit := exitCode(runErr)
		code := exit
		s.audit(ctx, audit.Event{Actor: actor, Action: "command_exit", Target: rec.Name, Command: command, Risk: risk, Decision: "allow", ExitCode: &code})
		return exit
	}

	client, cleanup, err := ui.DialSSHLeafForRecord(s.targetUser(rec, actor), rec)
	if err != nil {
		fmt.Fprintf(stderr, "error: connect: %v\n", err)
		recorder.RecordError(err)
		return 1
	}
	defer cleanup()

	sess, err := client.NewSession()
	if err != nil {
		fmt.Fprintf(stderr, "error: session: %v\n", err)
		recorder.RecordError(err)
		return 1
	}
	defer func() { _ = sess.Close() }()

	// Honor a requested PTY (ssh -t <resource> <cmd>): allocate a tty on the
	// target so its terminal driver does LF->CRLF (no "staircase" output) and
	// merges stderr into the pty stream, matching OpenSSH's `ssh -t host cmd`.
	if act.wantPTY {
		term := act.term
		if strings.TrimSpace(term) == "" {
			term = "xterm-256color"
		}
		modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
		rows, cols := act.rows, act.cols
		if rows <= 0 {
			rows = 24
		}
		if cols <= 0 {
			cols = 80
		}
		if perr := sess.RequestPty(term, rows, cols, modes); perr != nil {
			fmt.Fprintf(stderr, "error: request pty: %v\n", perr)
			recorder.RecordError(perr)
			return 1
		}
	}

	sess.Stdin = engine.WrapRecordingReader(ch, recorder, "in")
	// Mask outermost (closest to the target output) so the recorder and the
	// client both receive redacted bytes on stdout and stderr.
	outMask := NewMaskingWriter(engine.WrapRecordingWriter(ch, recorder, "out"), s.opts.MaskRules)
	errMask := NewMaskingWriter(engine.WrapRecordingWriter(stderr, recorder, "out"), s.opts.MaskRules)
	sess.Stdout = outMask
	sess.Stderr = errMask

	runErr := sess.Run(command)
	_ = outMask.Close()
	_ = errMask.Close()
	exit := exitCode(runErr)
	code := exit
	s.audit(ctx, audit.Event{Actor: actor, Action: "command_exit", Target: rec.Name, Command: command, Risk: risk, Decision: "allow", ExitCode: &code})
	return exit
}

// resolveResource maps a resource name to an inventory record via the injected
// records provider and the shared selector resolver.
func (s *Server) resolveResource(ctx context.Context, name string) (hosts.Record, error) {
	records, err := s.opts.Records(ctx)
	if err != nil {
		return hosts.Record{}, fmt.Errorf("inventory: %w", err)
	}
	rec, err := cuetry.ResolveHostFromRecords(name, records)
	if err != nil {
		return hosts.Record{}, err
	}
	return rec, nil
}

// guardModeVal resolves the configured interactive guardrail mode (off/audit/
// enforce), defaulting to off for empty or unknown values.
func (s *Server) guardModeVal() guardMode {
	return parseGuardMode(s.opts.GuardMode)
}

// gateInteractive asks OPA whether actor may open an interactive shell on rec
// (action "interactive_session"). A nil enforcer always allows. Mirrors the web
// server's gateInteractiveSession input shape.
func (s *Server) gateInteractive(ctx context.Context, actor string, rec hosts.Record) error {
	if s.opts.Enforcer == nil {
		return nil
	}
	d, err := s.opts.Enforcer.Evaluate(ctx, map[string]any{
		"action": "interactive_session",
		"actor":  actor,
		"target": targetInput(rec),
	})
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if !d.Allow {
		reason := d.DenyReason
		if reason == "" {
			reason = "forbidden by policy"
		}
		return errors.New(reason)
	}
	return nil
}

// targetUser resolves the login user for the target host. The authenticated
// actor (cert principal) is the authorization identity, not necessarily the
// target account, so the target login is record.Meta["ssh_user"] ->
// DefaultSSHUser -> actor (last-resort fallback). Mirrors the engine's
// precedence (internal/engine/ssh_dial.go).
func (s *Server) targetUser(rec hosts.Record, actor string) string {
	if u := strings.TrimSpace(rec.Meta["ssh_user"]); u != "" {
		return u
	}
	if u := strings.TrimSpace(s.opts.DefaultSSHUser); u != "" {
		return u
	}
	return actor
}

// commandPolicyInput builds the OPA input for a command_exec decision.
func commandPolicyInput(actor string, rec hosts.Record, command string) map[string]any {
	return map[string]any{
		"action":  "command_exec",
		"actor":   actor,
		"command": command,
		"target":  targetInput(rec),
	}
}

func targetInput(rec hosts.Record) map[string]any {
	return map[string]any{
		"name":     rec.Name,
		"provider": rec.Provider,
		"env":      rec.Meta["env"],
		"groups":   rec.Groups,
	}
}

// newRecorder creates a session recorder under RecordDir, or returns nil when
// recording is disabled or fails to start (the recorder API is nil-safe).
func (s *Server) newRecorder(rec hosts.Record, actor, mode string) *engine.SessionRecorder {
	if strings.TrimSpace(s.opts.RecordDir) == "" {
		return nil
	}
	r, err := engine.NewSessionRecorder(engine.SessionRecorderOptions{
		Dir:      s.opts.RecordDir,
		Trigger:  "ssh-gateway",
		Mode:     mode,
		Provider: rec.Provider,
		HostName: rec.Name,
		HostIP:   rec.PrimaryIP,
		User:     actor,
	})
	if err != nil {
		s.log.Warn("session recorder", zap.Error(err))
		return nil
	}
	return r
}

// audit logs one event with Source set, swallowing (logging) sink errors.
func (s *Server) audit(ctx context.Context, e audit.Event) {
	if s.opts.AuditSink == nil {
		return
	}
	e.Source = "ssh-gateway"
	if err := s.opts.AuditSink.Log(ctx, e); err != nil {
		s.log.Warn("audit sink", zap.Error(err))
	}
}

// reply answers an SSH request only when a reply was requested.
func reply(req *ssh.Request, ok bool) {
	if req.WantReply {
		_ = req.Reply(ok, nil)
	}
}

// sendExitStatus sends the standard exit-status request for the session.
func sendExitStatus(ch ssh.Channel, code int) {
	status := uint32(1)
	if code >= 0 && code <= 255 {
		status = uint32(code)
	}
	_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(exitStatusPayload{Status: status}))
}

// exitCode extracts the remote exit status from a session error.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *ssh.ExitError
	if errors.As(err, &ee) {
		return ee.ExitStatus()
	}
	return 1
}
