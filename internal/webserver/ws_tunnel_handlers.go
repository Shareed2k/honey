package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hostexec"
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
func (a *ForwardingAPI) handleWebTunnel(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
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

	user := a.sshUser(hello.SSHUser)
	if !hello.Record.IsConnectable() {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"record is not connectable"}`))
		return
	}

	target := strings.TrimSpace(hello.Target)
	if target == "" {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"missing target address"}`))
		return
	}

	// Target is caller-controlled; a "unix:<path>" target can reach any
	// server-side unix socket (SSRF-shaped), so gate it before dialing.
	if err := a.gateTunnel(r, target); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	// Dial the target using the appropriate executor
	executor := a.opts.ExecRegistry.ForRecord(hello.Record)
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

// gateTunnel asks OPA whether the actor may have the server dial target on
// their behalf (action "tunnel"). A nil enforcer always allows (parity with
// gateUDPRelay). Unlike the endpoint-level authMiddleware (which sees only the
// path), this passes the caller-controlled target — including a "unix:<path>"
// scheme that can reach any server-side unix socket — so a policy can restrict
// scheme and destination. Fails closed on evaluation error.
func (a *ForwardingAPI) gateTunnel(r *http.Request, target string) error {
	if a.opts.Enforcer == nil {
		return nil
	}
	pt, err := hostexec.ParseTunnelTarget(target)
	if err != nil {
		return fmt.Errorf("tunnel target: %w", err)
	}
	scheme := "tcp"
	if pt.Scheme == hostexec.TunnelUnix {
		scheme = "unix"
	}
	actor := userFromRequest(r, a.opts.TrustedProxyNets, a.opts.JWTPubKey)
	d, err := a.opts.Enforcer.Evaluate(r.Context(), map[string]any{
		"action": "tunnel",
		"actor":  actor,
		"target": map[string]any{
			"scheme": scheme,
			"dest":   pt.Dest,
			"host":   pt.Host,
			"port":   pt.Port,
		},
	})
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if !d.Allow {
		return fmt.Errorf("%s", reasonOrForbidden(d.DenyReason))
	}
	return nil
}
