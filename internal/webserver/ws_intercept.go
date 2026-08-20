package webserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/termguard"
)

// wsInterceptHello is the first frame a browser sends on /ws/intercept. It names
// the target pod record, the interception modes, and the initial terminal size.
// The actor is never taken from here — it comes from the authenticated session.
type wsInterceptHello struct {
	// Record is the target host record; it must be a Kubernetes pod (IsPod).
	Record hosts.Record `json:"record"`
	// Modes lists the interception modes to enable (egress|incoming|files|env).
	Modes []string `json:"modes"`
	// UDP includes the UDP tunnels alongside TCP.
	UDP bool `json:"udp"`
	// Command is the local command run under injection; empty ⇒ a shell.
	Command []string `json:"command,omitempty"`
	// Container is the target container the agent shares namespaces with; empty
	// selects the pod's first container (the CLI default).
	Container string `json:"container,omitempty"`
	// EnvInclude/EnvExclude carry env-mode key filters (names only, never values);
	// optional and mutually exclusive, mirroring the CLI flags.
	EnvInclude []string `json:"env_include,omitempty"`
	EnvExclude []string `json:"env_exclude,omitempty"`
	// Cols/Rows are the initial terminal size in character cells.
	Cols int `json:"cols"`
	Rows int `json:"rows"`
	// RecordSession requests an asciinema-style recording of the session.
	RecordSession bool `json:"record_session"`
}

// handleWebIntercept runs a browser-driven interception terminal: it deploys an
// OPA-gated, audited interception agent on the target pod and streams the
// injected shell's PTY over the WebSocket. It runs the DIRECT intercept.Session
// (not the Broker) with a custom PTY-bridging LocalRunner; the session's
// lifetime is tied to the WebSocket, so a client disconnect cancels the session
// context and tears the agent down.
func (s *Server) handleWebIntercept(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.opts.InterceptSessionFactory == nil {
		http.Error(w, "intercept is not enabled", http.StatusServiceUnavailable)
		return
	}
	// Actor is the authenticated session identity, never a client-supplied field.
	actor := userFromRequest(r, s.opts.TrustedProxyNets, s.opts.JWTPubKey)

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			zap.L().Error("web intercept panic", zap.Any("recover", rec))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"internal server error"}`))
		}
		_ = conn.Close()
	}()

	_, helloRaw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello wsInterceptHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}

	rec := hello.Record
	// Validate the hello: interception targets a Kubernetes pod only. Reject any
	// other record shape with a clear error before touching a cluster.
	if !rec.IsPod() {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"intercept requires a kubernetes pod record"}`))
		return
	}
	// Same interactive-session OPA gate the SSH terminal runs, on top of the
	// intercept gate that Session.Run itself enforces.
	if err := s.gateInteractiveSession(r, rec); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("session denied: "+err.Error()))
		return
	}

	// Resume path: with tmux on the server, run the interception inside a tmux
	// pane owned by `honey intercept-pane`, keyed by a deterministic per-pod
	// name so a browser refresh re-attaches to the same pane instead of starting
	// a second one. It is tmux-specific (a zellij on PATH does not count): the
	// resume list/cap/stop are tmux-based, so a zellij-hosted pane
	// could not be managed. Without tmux, fall back to today's in-process
	// one-shot session below (unchanged).
	if tmuxOnPath() {
		s.handleWebInterceptResume(conn, hello, rec, actor)
		return
	}

	opts, err := s.buildInterceptOptions(rec, hello, actor)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	// The session lifetime is bound to the WebSocket, not the (already-hijacked)
	// request context: a post-upgrade request-context cancel must not abort the
	// session, but a client disconnect must. Mirrors /ws/ssh's fresh context.
	sessionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Admit before deploying: enforce the concurrency cap and the same-pod guard
	// and register the session so it is counted, listed, and stoppable by id.
	// A rejection tears down nothing (no agent was deployed).
	sessID, err := s.webIntercepts.admit(sessionCtx, opts, cancel)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	// Deregister on every return path below (normal teardown or a build error).
	defer s.webIntercepts.remove(context.Background(), sessID)
	// Keep the registry entry's lease fresh so the TTL janitor never reaps a
	// live session. The heartbeat exits when sessionCtx is cancelled; the join
	// (with a cancel first, in case the session was never started) runs BEFORE
	// the deferred remove, so a late refresh cannot resurrect a deleted entry.
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		s.webIntercepts.heartbeat(sessionCtx, sessID)
	}()
	defer func() { cancel(); <-hbDone }()

	cols, rows := hello.Cols, hello.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	recorder := newWebSessionRecorder(s.opts.RecordDir, hello.RecordSession, rec, actor)
	if recorder != nil {
		recorder.RecordResize(cols, rows)
		defer recorder.Close()
	}

	// Bridge streams: browser stdin -> pipe -> injected child; child stdout ->
	// WS; browser resize -> local.Winsize channel.
	stdinR, stdinW := io.Pipe()
	resizeInts := make(chan [2]int, 32)
	winCh := make(chan local.Winsize, 32)
	wsOut := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	stdout := engine.WrapRecordingWriter(wsOut, recorder, "stdout")
	// Seed the initial size so the pty starts correctly before the first resize.
	winCh <- clampWinsize(cols, rows)

	// Same per-command interactive guardrail as /ws/ssh (internal/termguard):
	// off (the config default) makes NewReader return stdinR unchanged.
	guard := termGuardInputs{Enforcer: s.opts.Enforcer, Guardrails: s.opts.Guardrails, Actor: actor, Record: rec, AuditSink: s.opts.AuditSink, Mode: s.webGuardMode()}
	decide, onDecision := newTermGuardDecide(wsOut, guard)
	stdin := termguard.NewReader(sessionCtx, stdinR, wsOut, guard.Mode, decide, onDecision)

	runner := &wsPtyRunner{inner: s.interceptInnerRunner, stdin: stdin, stdout: stdout, resize: winCh}
	session, err := s.opts.InterceptSessionFactory(rec, opts, runner)
	if err != nil {
		_ = wsOut.writeText(`{"error":"` + escapeJSON(err.Error()) + `"}`)
		return
	}

	s.bridgeInterceptWS(sessionCtx, conn, session, cancel, stdinW, resizeInts, winCh, recorder, wsOut)
}

// buildInterceptOptions maps a validated pod hello to intercept.Options via the
// shared intercept.OptionsFromPodRecord mapper (the same mapping the
// intercept-pane subcommand uses), then sets the fields the mapper omits: the
// actor is passed in from the authenticated session; the agent image and
// Container/EnvInclude/EnvExclude come from the operator config and hello,
// never the client-controlled actor.
func (s *Server) buildInterceptOptions(rec hosts.Record, hello wsInterceptHello, actor string) (intercept.Options, error) {
	if len(hello.EnvInclude) > 0 && len(hello.EnvExclude) > 0 {
		return intercept.Options{}, errors.New("intercept: env_include and env_exclude are mutually exclusive")
	}
	agentImage := ""
	if s.opts.Config != nil && s.opts.Config.Intercept != nil {
		agentImage = strings.TrimSpace(s.opts.Config.Intercept.AgentImage)
	}
	opts, err := intercept.OptionsFromPodRecord(rec, hello.Modes, hello.UDP, hello.Command, agentImage)
	if err != nil {
		return intercept.Options{}, err
	}
	opts.Container = strings.TrimSpace(hello.Container)
	opts.Actor = actor
	opts.EnvInclude = hello.EnvInclude
	opts.EnvExclude = hello.EnvExclude
	return opts, nil
}

// interceptPaneRequestFromHello maps a validated pod hello to the pane's
// InterceptPaneRequest payload: the same fields buildInterceptOptions sets on
// the fallback path (Container/EnvInclude/EnvExclude/Actor), so the pane's
// own Options end up identical regardless of which path ran. Actor comes
// from the authenticated session, never the browser-supplied hello.
func interceptPaneRequestFromHello(rec hosts.Record, hello wsInterceptHello, actor string) InterceptPaneRequest {
	return InterceptPaneRequest{
		Record:     rec,
		Modes:      hello.Modes,
		UDP:        hello.UDP,
		Command:    hello.Command,
		Container:  hello.Container,
		EnvInclude: hello.EnvInclude,
		EnvExclude: hello.EnvExclude,
		Actor:      actor,
		Cols:       hello.Cols,
		Rows:       hello.Rows,
	}
}

// handleWebInterceptResume runs the interception inside a tmux/zellij pane
// owned by the hidden `honey intercept-pane` subcommand, reusing honey's
// existing SSH pty-proxy machinery (handleWebPtyProxy's template): it builds
// the pane's secret-free base64 payload, resolves an attach-or-create mux
// command keyed by a deterministic per-pod name, and bridges the pane's PTY
// to the browser exactly like the SSH terminal does. A browser refresh
// recomputes the same mux name and re-attaches to the same pane instead of
// starting a second interception.
//
// Session-cap/list/stop bookkeeping for this path is tmux-backed (the pane
// itself is the session record), so it does not call s.webIntercepts.admit; it
// does reuse admit's same-pod guard (samePodActive) so a pod already held by a
// brokered or fallback session is never intercepted twice.
func (s *Server) handleWebInterceptResume(conn *websocket.Conn, hello wsInterceptHello, rec hosts.Record, actor string) {
	payload, err := json.Marshal(interceptPaneRequestFromHello(rec, hello, actor))
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	enc := base64.StdEncoding.EncodeToString(payload)

	bin, err := os.Executable()
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	cfgPath, _ := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	// Trimmed to match intercept.OptionsFromPodRecord, so the same-pod guard
	// below compares against the exact values a registered session stores.
	cluster := strings.TrimSpace(rec.Meta["kube_context"])
	namespace := strings.TrimSpace(rec.Meta["namespace"])
	pod := strings.TrimSpace(rec.Meta["pod_name"])
	name := interceptPaneMuxName(cluster, namespace, pod)

	// Cap: a fresh resume session is admitted only under the concurrency cap; an
	// attach to a session that already exists bypasses it (the pane is reused, no
	// new session). List once and reuse `existed` for the metadata write below.
	// ponytail: no lock, so two concurrent starts for DISTINCT pods at the cap
	// boundary could overshoot by one — add a package mutex if that matters; a
	// human-paced action makes it not worth serializing every start behind a
	// subprocess spawn.
	existing := tmuxListHoneyIntercept()
	existed := false
	for _, si := range existing {
		if si.Name == name {
			existed = true
			break
		}
	}
	if !existed {
		maxSessions := config.DefaultMaxInterceptSessions
		if s.opts.Config != nil {
			maxSessions = s.opts.Config.Intercept.MaxSessionsValue()
		}
		if len(existing) >= maxSessions {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(fmt.Sprintf("intercept: max concurrent sessions (%d) reached — stop an active session before starting another", maxSessions))+`"}`))
			return
		}
		// Cross-pool same-pod guard: a brokered (or in-process fallback) session
		// lives in the shared SessionStore and has no tmux session, so the list
		// above cannot see it. Without this, a second agent would be deployed into
		// a pod that already has one and fight it for the fixed ports/nftables
		// table. Attaches (existed) skip it — that IS the first session.
		if s.webIntercepts != nil {
			same, err := s.webIntercepts.samePodActive(context.Background(), intercept.Options{Cluster: cluster, Namespace: namespace, Pod: pod})
			if err != nil {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
				return
			}
			if same {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(errSamePodActive(namespace, pod).Error())+`"}`))
				return
			}
		}
	}

	cmd, muxName, useZellij, err := ptyMuxBuildInterceptCommand(bin, cfgPath, enc, name)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	// Record secret-free metadata into the session's tmux environment so it is
	// listable/stoppable by the /sessions routes. Called unconditionally: it
	// no-ops when the metadata is already there (an attach keeps its original
	// start time) and repairs a session whose earlier write failed.
	interceptResumeSetMeta(muxName, pod, namespace, cluster, actor, strings.Join(hello.Modes, ","))

	recorder := newWebSessionRecorder(s.opts.RecordDir, hello.RecordSession, rec, actor)
	if recorder != nil {
		recorder.RecordResize(hello.Cols, hello.Rows)
		defer recorder.Close()
	}

	// FIX-2: guard the operator's ptmx writes here too — handleWebIntercept's
	// resume path is a mux path exactly like the SSH terminal's, so
	// web.guard_mode must reach it the same way.
	guard := termGuardInputs{Enforcer: s.opts.Enforcer, Guardrails: s.opts.Guardrails, Actor: actor, Record: rec, AuditSink: s.opts.AuditSink, Mode: s.webGuardMode()}
	closeTabKill := make(chan struct{}, 1)
	ptyExited := ptyProxyRunBridge(ptmx, conn, recorder, WSHello{Cols: hello.Cols, Rows: hello.Rows}, muxName, closeTabKill, ptyProxyStdinPolicy{OperatorGuard: &guard})
	ptyProxyTeardown(ptmx, cmd, muxName, useZellij, closeTabKill, ptyExited, interceptResumeCloseTabKill(muxName), false)
}

// bridgeInterceptWS runs the session and the WebSocket<->PTY bridge to
// completion, joining every goroutine before it returns.
//
// Goroutine lifecycle (all bounded, goleak-clean):
//   - the session runner exits when Session.Run returns (child exit, runner
//     error, or sessionCtx cancel);
//   - the WS read pump (pumpWebSocketToStreams) exits when the client
//     disconnects or when conn.Close below unblocks its ReadMessage, and on exit
//     it closes stdinW and resizeInts and cancels the session;
//   - the resize translator exits when the pump closes resizeInts, and then
//     closes winCh so mogate's resize pump ends.
//
// Whichever side ends first, the other is forced down: a client disconnect
// cancels sessionCtx (Session.Run drains and deletes the ephemeral container); a
// session exit closes the conn so the pump returns.
func (s *Server) bridgeInterceptWS(
	sessionCtx context.Context,
	conn *websocket.Conn,
	session *intercept.Session,
	cancel context.CancelFunc,
	stdinW *io.PipeWriter,
	resizeInts chan [2]int,
	winCh chan local.Winsize,
	recorder *engine.SessionRecorder,
	wsOut *wsWriter,
) {
	var wg sync.WaitGroup

	// resize translator: [cols,rows] -> local.Winsize. Closing winCh when the
	// pump closes resizeInts ends mogate's resize pump (it watches winCh).
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(winCh)
		for rc := range resizeInts {
			select {
			case winCh <- clampWinsize(rc[0], rc[1]):
			default:
			}
		}
	}()

	sessDone := make(chan error, 1)
	go func() { sessDone <- session.Run(sessionCtx) }()

	// WS read pump: browser -> stdin pipe + resize ints. It closes stdinW and
	// resizeInts on return; cancel the session so a client disconnect tears the
	// agent down.
	wg.Add(1)
	go func() {
		defer wg.Done()
		pumpWebSocketToStreams(conn, stdinW, resizeInts, recorder)
		cancel()
	}()

	waitErr := <-sessDone
	cancel()           // idempotent: ensures the session is cancelled if it ended first
	_ = stdinW.Close() // release mogate's stdin pump (also closed by the pump on WS close)

	// Report the outcome BEFORE closing the conn: the close below tears the
	// socket down, so anything written after it is lost and the browser sees a
	// bare 1006 with no reason (a session error, e.g. a failed agent deploy,
	// would be computed and then silently dropped).
	if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
		recorder.RecordError(waitErr)
		_ = wsOut.writeText(`{"closed":true,"error":"` + escapeJSON(waitErr.Error()) + `"}`)
	} else {
		_ = wsOut.writeText(`{"closed":true}`)
	}

	_ = conn.Close() // unblock the WS pump's ReadMessage so it returns
	wg.Wait()        // join the pump and the resize translator
}

// wsPtyRunner is the intercept.LocalRunner that bridges the injected child's PTY
// to a browser WebSocket: it forces a pseudo-terminal and wires the child's
// stdin/stdout/resize to the WS bridge, then delegates to the wrapped runner
// (production: intercept.DefaultLocalRunner → mogate local.Run). It never logs
// the config, which carries the relay socket path and token file location.
type wsPtyRunner struct {
	inner  intercept.LocalRunner
	stdin  io.Reader
	stdout io.Writer
	resize <-chan local.Winsize
}

// Run augments cfg with the PTY + browser streams and delegates to the inner
// runner. mogate closes an io.Closer stdin on ctx cancel to release its stdin
// pump; the handler also closes the pipe on teardown for cleanliness.
func (r *wsPtyRunner) Run(ctx context.Context, cfg local.Config, command []string) error {
	cfg.Pty = true
	cfg.Stdin = r.stdin
	cfg.Stdout = r.stdout
	cfg.Stderr = r.stdout
	cfg.ResizeCh = r.resize
	return r.inner.Run(ctx, cfg, command)
}

var _ intercept.LocalRunner = (*wsPtyRunner)(nil)

// clampWinsize converts a [cols, rows] pair to a local.Winsize, applying the
// same defaults as the SSH terminal and clamping to the uint16 range so the
// conversion is always in bounds (no unchecked numeric conversion).
func clampWinsize(cols, rows int) local.Winsize {
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
	return local.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
}
