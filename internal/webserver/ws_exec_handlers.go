package webserver

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hosts"
)

// WSExecHello is the initial message expected on an exec WebSocket connection.
type WSExecHello struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Command string       `json:"command"`
}

// handleWebExec provides a raw binary WebSocket stream for executing remote commands.
// It expects an initial WSExecHello JSON message, then blindly pumps binary frames
// to/from the upstream command's stdin/stdout/stderr.
// @Summary Stream remote command execution over WebSocket
// @Tags exec
// @Router /api/v1/ws/exec [get]
// @Security BearerAuth
func (s *Server) handleWebExec(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Read hello message
	_, helloRaw, err := conn.ReadMessage()
	if err != nil {
		return
	}

	var hello WSExecHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}

	user := s.sshUser(hello.SSHUser)
	if !hello.Record.IsConnectable() {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"record is not connectable"}`))
		return
	}

	client, err := s.fileClientCache.GetOrDial(user, hello.Record)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	// Note: We don't close the client here since it's managed by the cache.

	// Send success signal
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"connected"}`)); err != nil {
		return
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	errc := make(chan error, 4)

	// WebSocket -> Stdin
	go func() {
		defer stdinW.Close()
		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if mt == websocket.BinaryMessage {
				if _, err := stdinW.Write(p); err != nil {
					errc <- err
					return
				}
			}
		}
	}()

	// Stdout -> WebSocket
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stdoutR.Read(buf)
			if n > 0 {
				// Send as binary (0x01 = stdout prefix, 0x02 = stderr prefix if we wanted to multiplex,
				// but let's just send binary frames since honeyprovider doesn't currently demux)
				// For simplicity, we'll just multiplex them by sending stdout as Text and stderr as Binary
				// or just combine them if we want to keep it simple. Let's combine for now since RunWithStreams
				// is often used without strict demuxing in this simple implementation.
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	// Stderr -> WebSocket
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := stderrR.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	// Execute command
	go func() {
		err := client.RunWithStreams(hello.Command, stdinR, stdoutW, stderrW)
		_ = stdoutW.Close()
		_ = stderrW.Close()
		if err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		}
		errc <- err
	}()

	<-errc
}
