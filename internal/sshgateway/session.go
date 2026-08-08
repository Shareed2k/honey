package sshgateway

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
)

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
			start <- startAction{wantPTY: wantPTY, cols: cols, rows: rows}
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
			start <- startAction{isExec: true, command: p.Command, wantPTY: wantPTY, cols: cols, rows: rows}
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
	return s.runExec(ctx, ch, actor, rec, remainder)
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

	runErr := ui.RunSSHInteractiveStreams(ctx, targetUser, rec, stdin, mw, cols, rows, uiResize)
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
func (s *Server) runExec(ctx context.Context, ch ssh.Channel, actor string, rec hosts.Record, command string) int {
	stderr := ch.Stderr()

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
