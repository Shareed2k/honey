package webserver

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/truenasshell"
	"github.com/shareed2k/honey/internal/ui"
	"go.uber.org/zap"
)

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     websocketSameHostOrigin,
}

// websocketSameHostOrigin allows browser WebSockets when the Origin host matches the
// request Host (any port). The previous localhost-only check broke real hostnames,
// IPs, and Ingress / port-forward setups that are not literally "localhost".
func websocketSameHostOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	ou, err := url.Parse(origin)
	if err != nil || ou.Host == "" {
		return false
	}
	reqHost := hostOnly(r.Host)
	origHost := hostOnly(ou.Host)
	return reqHost != "" && origHost != "" && reqHost == origHost
}

func hostOnly(hostPort string) string {
	hostPort = strings.TrimSpace(strings.ToLower(hostPort))
	if hostPort == "" {
		return ""
	}
	h, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort
	}
	return h
}

// WSHello is exported so it can be unmarshaled by the honey pty-proxy subcommand.
type WSHello struct {
	SessionID     string       `json:"session_id"`
	SSHUser       string       `json:"ssh_user"`
	Record        hosts.Record `json:"record"`
	Cols          int          `json:"cols"`
	Rows          int          `json:"rows"`
	RecordSession bool         `json:"record_session"`
	Console       string       `json:"console,omitempty"` // "truenas_api" for TrueNAS /websocket/shell
}

type wsResize struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Server) handleWebSSH(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			zap.L().Error("web terminal panic", zap.Any("recover", rec))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"internal server error"}`))
		}
		_ = conn.Close()
	}()

	_, helloRaw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello WSHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}
	user := s.sshUser(hello.SSHUser)
	hello.SSHUser = user
	if patched, err := json.Marshal(hello); err == nil {
		helloRaw = patched
	}

	if err := s.gateInteractiveSession(r, hello.Record); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("session denied: "+err.Error()))
		return
	}
	cols, rows := hello.Cols, hello.Rows
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	recorder := newWebSessionRecorder(s.opts.RecordDir, hello.RecordSession, hello.Record, user)
	if recorder != nil {
		recorder.RecordResize(cols, rows)
		defer recorder.Close()
	}

	if shouldUseWebPtyProxy(hello) {
		zap.L().Debug("web ssh: session ID provided, attempting pty proxy", zap.String("session_id", hello.SessionID))
		err := handleWebPtyProxy(conn, helloRaw, hello, recorder, s.opts.ConfigPath)
		if err == nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true}`))
			return
		}
		// If error is that tmux/zellij aren't found, we just fallback to the normal ephemeral shell below!
		if err.Error() != "neither zellij nor tmux found on the server" {
			zap.L().Error("web ssh: pty proxy failed", zap.Error(err))
			recorder.RecordError(err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
			return
		}
		zap.L().Debug("web ssh: pty proxy fallback triggered")
	}

	// Interactive terminal dispatch runs entirely through the executor seam.
	//
	// A record this node proxies to another node (honey mesh/direct) is
	// forwarded wholesale first: the terminating server resolves it locally and
	// dispatches to the right native shell/console. hostexec.ProxyExecutor is how
	// the seam declares "I forward this elsewhere", so the webserver never names a
	// provider or strips routing metadata to make resolution work.
	rec := hello.Record
	ex := s.opts.ExecRegistry.ForRecord(rec)
	if hostexec.IsProxy(ex) {
		defer s.trackWSConnection("honey_upstream")()
		serveWebInteractive(conn, ex, user, rec, cols, rows, recorder)
		return
	}

	// Provider-specific consoles (serial/API bridges, not exec shells) are served
	// by the node that owns the record, ahead of the generic exec seam below.
	if isProxmoxSerialWebPVE(rec) {
		defer s.trackWSConnection("pve_serial")()
		handleWebProxmoxPVESerialTTY(context.Background(), conn, rec, cols, rows, recorder)
		return
	}
	if truenasshell.ShouldUseTrueNASShell(rec, hello.Console) {
		defer s.trackWSConnection("truenas_shell")()
		handleWebTrueNASShellTTY(context.Background(), conn, rec, cols, rows, recorder)
		return
	}

	// docker / k8s / ssh all resolve to a hostexec.InteractiveStreamer via the
	// seam; a non-cancelled context is used so a post-hijack request-context
	// cancel cannot abort a SPDY/exec stream immediately.
	defer s.trackWSConnection(interactiveWSKind(rec))()
	serveWebInteractive(conn, ex, user, rec, cols, rows, recorder)
}

// shouldUseWebPtyProxy reports whether to wrap the session in local tmux/zellij so a
// browser refresh can re-attach (SSH, Docker, Kubernetes pods, and TrueNAS API shells).
func shouldUseWebPtyProxy(hello WSHello) bool {
	return strings.TrimSpace(hello.SessionID) != ""
}

func benignDockerWSExit(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "use of closed network connection")
}

// serveWebInteractive runs the browser terminal for rec through ex when it
// supports interactive streaming (docker/k8s/ssh locally, or the honey proxy for
// a mesh-routed record), or reports a clear error when no such path exists.
func serveWebInteractive(conn *websocket.Conn, ex hostexec.Executor, user string, rec hosts.Record, cols, rows int, recorder *engine.SessionRecorder) {
	is, ok := ex.(hostexec.InteractiveStreamer)
	if !ok {
		// The resolved executor can't stream a terminal — e.g. a registry that
		// only wires exec/tunnel, or a plain SSH host the registry doesn't claim
		// interactively. Fall back to a direct leaf-SSH shell, the universal path
		// a host always had before the seam unified the dispatch (independent of
		// the exec registry).
		is = sshFallbackStreamer{}
	}
	handleWebInteractiveStreams(context.Background(), conn, is, user, rec, cols, rows, recorder)
}

// sshFallbackStreamer is the universal SSH terminal: it dials the record's leaf
// SSH directly (ui.RunSSHInteractiveStreams), independent of the executor
// registry. Used by serveWebInteractive when Registry.ForRecord yields a
// non-interactive executor, preserving the pre-seam behavior that any host with
// an IP gets a shell. The SSH PTY plumbing itself lives once in the ui package,
// shared with cli's sshFallbackExecutor.
type sshFallbackStreamer struct{}

func (sshFallbackStreamer) RunInteractiveStreams(ctx context.Context, user string, r hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error {
	return ui.RunSSHInteractiveStreams(ctx, user, r, stdin, stdout, cols, rows, resize)
}

var _ hostexec.InteractiveStreamer = sshFallbackStreamer{}

// interactiveWSKind labels the local shell path for connection metrics.
func interactiveWSKind(rec hosts.Record) string {
	if rec.Provider == "k8s" && strings.EqualFold(rec.Meta["kind"], "pod") {
		return "k8s"
	}
	if rec.IsDocker() {
		return "docker"
	}
	return "ssh"
}

// handleWebInteractiveStreams runs a browser terminal against any executor that
// implements hostexec.InteractiveStreamer: pipe browser stdin in, stream stdout
// out, forward resizes as [cols,rows], report closure. This one lifecycle covers
// docker, k8s, ssh, and the honey upstream proxy (which forwards to the server
// that owns the record and dispatches to the right native shell there).
func handleWebInteractiveStreams(ctx context.Context, conn *websocket.Conn, is hostexec.InteractiveStreamer, user string, rec hosts.Record, cols, rows int, recorder *engine.SessionRecorder) {
	stdinPipeR, stdinPipeW := io.Pipe()
	resizeCh := make(chan [2]int, 32)
	wsOut := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	stdout := engine.WrapRecordingWriter(wsOut, recorder, "stdout")

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- is.RunInteractiveStreams(ctx, user, rec, stdinPipeR, stdout, cols, rows, resizeCh)
	}()

	go pumpWebSocketToStreams(conn, stdinPipeW, resizeCh, recorder)

	waitErr := <-waitDone
	_ = stdinPipeW.Close()
	if waitErr != nil && !benignDockerWSExit(waitErr) {
		recorder.RecordError(waitErr)
		_ = wsOut.writeText(`{"closed":true,"error":"` + escapeJSON(waitErr.Error()) + `"}`)
	} else {
		_ = wsOut.writeText(`{"closed":true}`)
	}
}

// pumpWebSocketToStreams forwards browser WS frames to an InteractiveStreamer:
// BinaryMessage -> stdin pipe, resize TextMessage -> resize chan as [cols, rows].
// Closes resizeCh on exit so the executor's resize consumer ends.
func pumpWebSocketToStreams(conn *websocket.Conn, stdinPipeW *io.PipeWriter, resizeCh chan<- [2]int, recorder *engine.SessionRecorder) {
	defer close(resizeCh)
	for {
		mt, payload, err := conn.ReadMessage()
		if err != nil {
			recorder.RecordError(err)
			_ = stdinPipeW.CloseWithError(err)
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			recorder.RecordData("stdin", payload)
			if _, werr := stdinPipeW.Write(payload); werr != nil {
				recorder.RecordError(werr)
				return
			}
		case websocket.TextMessage:
			var rz wsResize
			if json.Unmarshal(payload, &rz) != nil || rz.Type != "resize" {
				continue
			}
			if rz.Cols > 0 && rz.Rows > 0 {
				recorder.RecordResize(rz.Cols, rz.Rows)
				select {
				case resizeCh <- [2]int{rz.Cols, rz.Rows}:
				default:
				}
			}
		}
	}
}

type wsWriter struct {
	conn *websocket.Conn
	mu   *sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *wsWriter) writeText(payload string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.TextMessage, []byte(payload))
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

func newWebSessionRecorder(recordDir string, requestRecord bool, rec hosts.Record, user string) *engine.SessionRecorder {
	dir := strings.TrimSpace(recordDir)
	if dir == "" || !requestRecord {
		return nil
	}
	mode := "ssh"
	switch {
	case rec.Provider == "k8s" && strings.EqualFold(rec.Meta["kind"], "pod"):
		mode = "k8s"
	case rec.IsDocker():
		mode = "docker"
	case isProxmoxSerialWebPVE(rec):
		mode = "proxmox"
	}
	r, err := engine.NewSessionRecorder(engine.SessionRecorderOptions{
		Dir:      dir,
		Trigger:  "web",
		Mode:     mode,
		Provider: rec.Provider,
		HostName: rec.Name,
		HostIP:   rec.PrimaryIP,
		User:     user,
	})
	if err != nil {
		return nil
	}
	return r
}
