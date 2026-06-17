package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/truenasshell"
	"github.com/shareed2k/honey/internal/ui"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/tools/remotecommand"
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

	if isK8sPodWebTerminal(hello.Record) {
		// Use a non-cancelled context: the HTTP request context can be cancelled after hijack
		// in some setups, which would abort the SPDY exec stream immediately.
		defer s.trackWSConnection("k8s")()
		handleWebK8sTTY(context.Background(), conn, hello.Record, cols, rows, recorder)
		return
	}

	if hosts.IsDockerRecord(hello.Record) {
		defer s.trackWSConnection("docker")()
		handleWebDockerTTY(context.Background(), conn, user, hello.Record, cols, rows, recorder, s.opts.ExecRegistry)
		return
	}

	if isProxmoxSerialWebPVE(hello.Record) {
		defer s.trackWSConnection("pve_serial")()
		handleWebProxmoxPVESerialTTY(context.Background(), conn, hello.Record, cols, rows, recorder)
		return
	}

	if truenasshell.ShouldUseTrueNASShell(hello.Record, hello.Console) {
		defer s.trackWSConnection("truenas_shell")()
		handleWebTrueNASShellTTY(context.Background(), conn, hello.Record, cols, rows, recorder)
		return
	}

	defer s.trackWSConnection("ssh")()
	client, cleanup, err := ui.DialSSHLeafForRecord(user, hello.Record)
	if err != nil {
		recorder.RecordError(err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	defer cleanup()

	sess, err := client.NewSession()
	if err != nil {
		recorder.RecordError(err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	defer func() { _ = sess.Close() }()

	var shellCmd string
	env, err := cuetry.EffectiveEnvForRun(context.Background(), false, nil, &cuetry.StepBase{}, nil, nil, &hello.Record)
	if err == nil && len(env) > 0 {
		for k, v := range env {
			_ = sess.Setenv(k, v)
		}
		shellCmd, _ = cuetry.ShellExportPrefixForRemote(env, `exec "${SHELL:-sh}" -l || exec "${SHELL:-sh}"`)
	}

	modes := ssh.TerminalModes{ssh.ECHO: 1, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm-256color", rows, cols, modes); err != nil {
		recorder.RecordError(err)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	stdinPipeR, stdinPipeW := io.Pipe()
	sess.Stdin = stdinPipeR
	outWriter := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	sess.Stdout = engine.WrapRecordingWriter(outWriter, recorder, "stdout")
	sess.Stderr = engine.WrapRecordingWriter(outWriter, recorder, "stderr")

	if shellCmd != "" {
		if err := sess.Start(shellCmd); err != nil {
			recorder.RecordError(err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
			return
		}
	} else {
		if err := sess.Shell(); err != nil {
			recorder.RecordError(err)
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
			return
		}
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- sess.Wait()
	}()

	go pumpWebSocketToStdin(conn, stdinPipeW, sess, recorder)

	waitErr := <-waitDone
	_ = stdinPipeW.Close()
	if waitErr != nil {
		recorder.RecordError(waitErr)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true,"error":"`+escapeJSON(waitErr.Error())+`"}`))
	} else {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true}`))
	}
}

// shouldUseWebPtyProxy reports whether to wrap the session in local tmux/zellij so a
// browser refresh can re-attach (SSH, Docker, Kubernetes pods, and TrueNAS API shells).
func shouldUseWebPtyProxy(hello WSHello) bool {
	return strings.TrimSpace(hello.SessionID) != ""
}

func isK8sPodWebTerminal(rec hosts.Record) bool {
	if rec.Provider != "k8s" {
		return false
	}
	if !strings.EqualFold(rec.Meta["kind"], "pod") {
		return false
	}
	if strings.TrimSpace(rec.Meta["namespace"]) == "" || strings.TrimSpace(rec.Meta["pod_name"]) == "" {
		return false
	}
	return true
}

func handleWebDockerTTY(ctx context.Context, conn *websocket.Conn, user string, rec hosts.Record, cols, rows int, recorder *engine.SessionRecorder, reg hostexec.Registry) {
	wsOut := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	if strings.TrimSpace(rec.Meta["container_id"]) == "" {
		err := fmt.Errorf("docker record missing container_id")
		recorder.RecordError(err)
		_ = wsOut.writeText(`{"error":"` + escapeJSON(err.Error()) + `"}`)
		return
	}
	if err := engine.DialDockerCheck(user, rec, reg); err != nil {
		recorder.RecordError(err)
		_ = wsOut.writeText(`{"error":"` + escapeJSON(err.Error()) + `"}`)
		return
	}
	stdinPipeR, stdinPipeW := io.Pipe()
	resizeCh := make(chan ui.DockerTerminalSize, 32)
	stdout := engine.WrapRecordingWriter(wsOut, recorder, "stdout")

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- ui.RunDockerWebTTY(ctx, user, rec, stdinPipeR, stdout, cols, rows, resizeCh, reg)
	}()

	go pumpWebSocketToStdinDocker(conn, stdinPipeW, resizeCh, recorder)

	waitErr := <-waitDone
	_ = stdinPipeW.Close()
	if waitErr != nil && !benignDockerWSExit(waitErr) {
		recorder.RecordError(waitErr)
		_ = wsOut.writeText(`{"closed":true,"error":"` + escapeJSON(waitErr.Error()) + `"}`)
	} else {
		_ = wsOut.writeText(`{"closed":true}`)
	}
}

func benignDockerWSExit(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context canceled") || strings.Contains(msg, "use of closed network connection")
}

func pumpWebSocketToStdinDocker(conn *websocket.Conn, stdinPipeW *io.PipeWriter, resizeCh chan<- ui.DockerTerminalSize, recorder *engine.SessionRecorder) {
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
			c, rw := rz.Cols, rz.Rows
			if c > 0 && rw > 0 {
				recorder.RecordResize(c, rw)
				select {
				case resizeCh <- ui.DockerTerminalSize{Cols: c, Rows: rw}:
				default:
				}
			}
		}
	}
}

func handleWebK8sTTY(ctx context.Context, conn *websocket.Conn, rec hosts.Record, cols, rows int, recorder *engine.SessionRecorder) {
	stdinPipeR, stdinPipeW := io.Pipe()
	resizeCh := make(chan *remotecommand.TerminalSize, 32)
	outWriter := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	stdout := engine.WrapRecordingWriter(outWriter, recorder, "stdout")
	stderr := engine.WrapRecordingWriter(outWriter, recorder, "stderr")
	stdin := io.Reader(stdinPipeR)

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- ui.RunK8sPodWebTTY(ctx, rec, stdin, stdout, stderr, cols, rows, resizeCh)
	}()

	go pumpWebSocketToStdinK8s(conn, stdinPipeW, resizeCh, recorder)

	waitErr := <-waitDone
	_ = stdinPipeW.Close()
	if waitErr != nil {
		recorder.RecordError(waitErr)
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true,"error":"`+escapeJSON(waitErr.Error())+`"}`))
	} else {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"closed":true}`))
	}
}

func pumpWebSocketToStdinK8s(conn *websocket.Conn, stdinPipeW *io.PipeWriter, resizeCh chan<- *remotecommand.TerminalSize, recorder *engine.SessionRecorder) {
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
			c, rw := rz.Cols, rz.Rows
			if sz := ui.ResizeFromColsRows(c, rw); sz != nil {
				recorder.RecordResize(c, rw)
				select {
				case resizeCh <- sz:
				default:
				}
			}
		}
	}
}

func pumpWebSocketToStdin(conn *websocket.Conn, stdinPipeW *io.PipeWriter, sess *ssh.Session, recorder *engine.SessionRecorder) {
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
			c, rw := rz.Cols, rz.Rows
			if c > 0 && rw > 0 {
				recorder.RecordResize(c, rw)
				_ = sess.WindowChange(rw, c)
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
	case isK8sPodWebTerminal(rec):
		mode = "k8s"
	case hosts.IsDockerRecord(rec):
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
