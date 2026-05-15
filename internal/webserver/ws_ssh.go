package webserver

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
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
}

type wsResize struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Server) handleWebSSH(w http.ResponseWriter, r *http.Request) {
	if !tokenFromRequest(r, s.opts.Token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	_, helloRaw, err := conn.ReadMessage()
	if err != nil {
		return
	}
	var hello WSHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}
	user := strings.TrimSpace(hello.SSHUser)
	if user == "" {
		user = os.Getenv("USER")
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

	if hello.SessionID != "" {
		zap.L().Debug("web ssh: session ID provided, attempting pty proxy", zap.String("session_id", hello.SessionID))
		err := handleWebPtyProxy(conn, helloRaw, hello, recorder)
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
		handleWebK8sTTY(context.Background(), conn, hello.Record, cols, rows, recorder)
		return
	}

	if isProxmoxSerialWebPVE(hello.Record) {
		handleWebProxmoxPVESerialTTY(context.Background(), conn, hello.Record, cols, rows, recorder)
		return
	}

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
	env, err := cuetry.EffectiveEnvForRun(cuetry.RecipeStep{}, nil, nil, &hello.Record)
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
	sess.Stdout = ui.WrapRecordingWriter(outWriter, recorder, "stdout")
	sess.Stderr = ui.WrapRecordingWriter(outWriter, recorder, "stderr")

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

func handleWebK8sTTY(ctx context.Context, conn *websocket.Conn, rec hosts.Record, cols, rows int, recorder *ui.SessionRecorder) {
	stdinPipeR, stdinPipeW := io.Pipe()
	resizeCh := make(chan *remotecommand.TerminalSize, 32)
	outWriter := &wsWriter{conn: conn, mu: &sync.Mutex{}}
	stdout := ui.WrapRecordingWriter(outWriter, recorder, "stdout")
	stderr := ui.WrapRecordingWriter(outWriter, recorder, "stderr")
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

func pumpWebSocketToStdinK8s(conn *websocket.Conn, stdinPipeW *io.PipeWriter, resizeCh chan<- *remotecommand.TerminalSize, recorder *ui.SessionRecorder) {
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

func pumpWebSocketToStdin(conn *websocket.Conn, stdinPipeW *io.PipeWriter, sess *ssh.Session, recorder *ui.SessionRecorder) {
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

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

func newWebSessionRecorder(recordDir string, requestRecord bool, rec hosts.Record, user string) *ui.SessionRecorder {
	dir := strings.TrimSpace(recordDir)
	if dir == "" || !requestRecord {
		return nil
	}
	mode := "ssh"
	if isK8sPodWebTerminal(rec) {
		mode = "k8s"
	} else if isProxmoxSerialWebPVE(rec) {
		mode = "proxmox"
	}
	r, err := ui.NewSessionRecorder(ui.SessionRecorderOptions{
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
