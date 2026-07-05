package webserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
)

// WSTunnelHello is the initial message expected on a tunnel WebSocket connection.
type WSTunnelHello struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Target  string       `json:"target"` // The target address to dial (e.g. "127.0.0.1:8080")
}

// handleWebTunnel provides a raw binary WebSocket stream for tunneling TCP connections.
// It expects an initial WSTunnelHello JSON message, then blindly pumps binary frames
// to/from the upstream connection.
// @Summary Open TCP tunnel over WebSocket
// @Tags tunnels
// @Router /api/v1/ws/tunnel [get]
// @Security BearerAuth
func (s *Server) handleWebTunnel(w http.ResponseWriter, r *http.Request) {
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

	var hello WSTunnelHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}

	user := s.sshUser(hello.SSHUser)
	if !hello.Record.IsConnectable() {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"record is not connectable"}`))
		return
	}

	target := strings.TrimSpace(hello.Target)
	if target == "" {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"missing target address"}`))
		return
	}

	// Dial the target using the appropriate executor
	executor := s.opts.ExecRegistry.ForRecord(hello.Record)
	if executor == nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"no executor found for record"}`))
		return
	}

	upstream, err := executor.DialUpstream(r.Context(), user, hello.Record, target)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	defer upstream.Close()

	// Send success signal
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"connected"}`)); err != nil {
		return
	}

	// Pump traffic
	errc := make(chan error, 2)

	// WebSocket -> Upstream
	go func() {
		for {
			mt, p, err := conn.ReadMessage()
			if err != nil {
				errc <- err
				return
			}
			if mt == websocket.BinaryMessage {
				if _, err := upstream.Write(p); err != nil {
					errc <- err
					return
				}
			}
		}
	}()

	// Upstream -> WebSocket
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := upstream.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					errc <- werr
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					zap.L().Debug("tunnel upstream read error", zap.Error(err))
				}
				errc <- err
				return
			}
		}
	}()

	<-errc
}
