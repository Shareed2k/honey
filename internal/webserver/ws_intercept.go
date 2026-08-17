package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/intercept"
)

// defaultInterceptShell is the command run under injection when the hello does
// not name one: a plain interactive shell, so the browser terminal behaves like
// `honey intercept <pod> -- /bin/sh`.
var defaultInterceptShell = []string{"/bin/sh"}

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

	opts, err := s.buildInterceptOptions(rec, hello, actor)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

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

	// The session lifetime is bound to the WebSocket, not the (already-hijacked)
	// request context: a post-upgrade request-context cancel must not abort the
	// session, but a client disconnect must. Mirrors /ws/ssh's fresh context.
	sessionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Bridge streams: browser stdin -> pipe -> injected child; child stdout ->
	// WS; browser resize -> local.Winsize channel.
	stdinR, stdinW := io.Pipe()
	resizeInts := make(chan [2]int, 32)
	winCh := make(chan local.Winsize, 32)
	wsOut := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	stdout := engine.WrapRecordingWriter(wsOut, recorder, "stdout")
	// Seed the initial size so the pty starts correctly before the first resize.
	winCh <- clampWinsize(cols, rows)

	runner := &wsPtyRunner{inner: s.interceptInnerRunner, stdin: stdinR, stdout: stdout, resize: winCh}
	session, err := s.opts.InterceptSessionFactory(rec, opts, runner)
	if err != nil {
		_ = wsOut.writeText(`{"error":"` + escapeJSON(err.Error()) + `"}`)
		return
	}

	s.bridgeInterceptWS(sessionCtx, conn, session, cancel, stdinW, resizeInts, winCh, recorder, wsOut)
}

// buildInterceptOptions maps a validated pod hello to intercept.Options. The
// actor is passed in from the authenticated session; the agent image comes from
// the operator config, never the client.
func (s *Server) buildInterceptOptions(rec hosts.Record, hello wsInterceptHello, actor string) (intercept.Options, error) {
	namespace := strings.TrimSpace(rec.Meta["namespace"])
	pod := strings.TrimSpace(rec.Meta["pod_name"])
	if namespace == "" || pod == "" {
		return intercept.Options{}, errors.New("intercept: pod record missing namespace or pod_name")
	}
	if len(hello.EnvInclude) > 0 && len(hello.EnvExclude) > 0 {
		return intercept.Options{}, errors.New("intercept: env_include and env_exclude are mutually exclusive")
	}
	modes, err := intercept.ParseModes(hello.Modes)
	if err != nil {
		return intercept.Options{}, err
	}
	agentImage := ""
	if s.opts.Config != nil && s.opts.Config.Intercept != nil {
		agentImage = strings.TrimSpace(s.opts.Config.Intercept.AgentImage)
	}
	if agentImage == "" {
		return intercept.Options{}, errors.New("intercept: no agent image configured (set intercept.agent_image)")
	}
	command := hello.Command
	if len(command) == 0 {
		command = defaultInterceptShell
	}
	return intercept.Options{
		Namespace:  namespace,
		Pod:        pod,
		Container:  strings.TrimSpace(hello.Container),
		Cluster:    strings.TrimSpace(rec.Meta["kube_context"]),
		AgentImage: agentImage,
		Modes:      modes,
		EnvInclude: hello.EnvInclude,
		EnvExclude: hello.EnvExclude,
		UDP:        hello.UDP,
		Command:    command,
		Actor:      actor,
		Targetless: false,
	}, nil
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
	_ = conn.Close()   // unblock the WS pump's ReadMessage so it returns
	wg.Wait()          // join the pump and the resize translator

	if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
		recorder.RecordError(waitErr)
		_ = wsOut.writeText(`{"closed":true,"error":"` + escapeJSON(waitErr.Error()) + `"}`)
		return
	}
	_ = wsOut.writeText(`{"closed":true}`)
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
