package webserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/udprelaywire"
)

// udpTarget is the minimal surface handleWebUDPRelay needs from a dialed UDP
// socket: read/write the datagram stream, close it, and bound a pending Read
// so the read goroutine can notice teardown (ctx cancel / target close /
// peer close) promptly instead of blocking forever. *net.UDPConn satisfies
// this (see the compile-time check below); tests inject a fake to avoid
// opening real sockets.
type udpTarget interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
}

// udpDialer opens a udpTarget for a validated "host:port" string. Production
// uses realUDPDialer (net.Dialer.DialContext); tests inject a fake so the
// handler can be exercised without a real UDP socket.
type udpDialer interface {
	DialUDP(ctx context.Context, target string) (udpTarget, error)
}

var _ udpTarget = (*net.UDPConn)(nil)

// realUDPDialer is the production udpDialer: it dials a real UDP socket via
// net.Dialer so DialUDP respects ctx cancellation while connecting.
type realUDPDialer struct{}

func (realUDPDialer) DialUDP(ctx context.Context, target string) (udpTarget, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", target)
	if err != nil {
		return nil, fmt.Errorf("dial udp %s: %w", target, err)
	}
	udpConn, ok := conn.(*net.UDPConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("dial udp %s: unexpected conn type %T", target, conn)
	}
	return udpConn, nil
}

var _ udpDialer = (*realUDPDialer)(nil)

// gateUDPRelay asks OPA whether actor may have the server originate UDP to
// target (action "udp_relay"). A nil enforcer always allows. Unlike the
// endpoint-level authMiddleware (which sees only the path), this passes the
// caller-controlled target host:port so a policy can restrict WHICH address
// the server will dial. target must already have passed ValidateTarget.
func (a *ForwardingAPI) gateUDPRelay(r *http.Request, target string) error {
	if a.opts.Enforcer == nil {
		return nil
	}
	host, port, _ := net.SplitHostPort(target)
	actor := userFromRequest(r, a.opts.TrustedProxyNets, a.opts.JWTPubKey)
	d, err := a.opts.Enforcer.Evaluate(r.Context(), map[string]any{
		"action": "udp_relay",
		"actor":  actor,
		"target": map[string]any{
			"host": host,
			"port": port,
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

// udpRelayIdleReadTimeout bounds a single udpTarget.Read call so the read
// goroutine wakes up periodically to check for teardown instead of blocking
// on Read forever; a timeout on its own is not fatal, the loop just reads
// again. This is what makes a blocking Read cancellable: an explicit
// target.Close() (from teardown) still unblocks it immediately, this
// deadline only bounds the case where nothing has happened yet.
const udpRelayIdleReadTimeout = 60 * time.Second

// udpRelayReadBufferSize matches udprelaywire's frame payload ceiling (the
// largest UDP datagram payload possible) so a single Read never truncates a
// maximal datagram.
const udpRelayReadBufferSize = 65507

// WSUDPRelayHello is the initial message expected on a UDP relay WebSocket
// connection.
type WSUDPRelayHello struct {
	Target string `json:"target"`
}

// handleWebUDPRelay bridges a single UDP socket -- dialed BY THE SERVER to
// target -- to a control WebSocket, one udprelaywire-framed datagram per WS
// BinaryMessage in each direction. This is the server-side vantage point for
// honeyprovider's UDP relay bridge: the caller picks the target, and the
// server sends/receives UDP traffic on the caller's behalf.
//
// SECURITY: target is caller-controlled. It is validated with
// udprelaywire.ValidateTarget before dialing, but this endpoint still lets
// an authorized caller make the server originate UDP traffic to an
// arbitrary host:port (SSRF-shaped). It is gated by the /api/v1
// authMiddleware (token + OPA on the request path) plus the explicit
// s.authorized(r) check below, same as the other privileged ws/* handlers --
// but neither of those sees the target. In addition, gateUDPRelay evaluates a
// target-aware OPA decision (action "udp_relay", including the target's host
// and port) after ValidateTarget succeeds and before dialing, so a policy can
// restrict WHICH host:port the server is allowed to dial, not just whether
// the endpoint is reachable. Do not weaken any of these.
//
// @Summary Open UDP relay over WebSocket
// @Tags tunnels
// @Router /api/v1/ws/udp [get]
// @Security BearerAuth
func (a *ForwardingAPI) handleWebUDPRelay(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	_, helloRaw, err := conn.ReadMessage()
	if err != nil {
		return
	}

	var hello WSUDPRelayHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}

	target := strings.TrimSpace(hello.Target)
	if err := udprelaywire.ValidateTarget(target); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	if err := a.gateUDPRelay(r, target); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	dialer := a.udpDialer
	if dialer == nil {
		dialer = realUDPDialer{}
	}
	udpConn, err := dialer.DialUDP(ctx, target)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	defer udpConn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"status":"connected"}`)); err != nil {
		return
	}

	runUDPRelayBridge(ctx, conn, udpConn)
}

// runUDPRelayBridge drives the bidirectional bridge between conn (the
// control WebSocket) and target (the dialed UDP socket) until either side
// ends or ctx is cancelled, then tears both down with no goroutine leak:
// teardown is idempotent (sync.OnceFunc) and this function does not return
// until both the ctx-watcher goroutine and the target-read goroutine have
// exited.
func runUDPRelayBridge(ctx context.Context, conn *websocket.Conn, target udpTarget) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup

	teardown := sync.OnceFunc(func() {
		cancel()
		_ = target.Close()
		_ = conn.Close()
	})
	defer teardown()

	// Cancel-driven teardown (server shutdown / request ctx done, or a
	// fatal error surfaced by the read pump below).
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		teardown()
	}()

	// target -> WS: read datagrams from the UDP target, frame them, and
	// forward each as one WS BinaryMessage.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer teardown()
		pumpUDPTargetToWS(ctx, conn, target)
	}()

	// WS -> target: each BinaryMessage is one udprelaywire-framed datagram.
	for {
		mt, p, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		payload, ferr := udprelaywire.ReadFrame(bytes.NewReader(p))
		if ferr != nil {
			zap.L().Debug("udp relay: malformed frame from client", zap.Error(ferr))
			break
		}
		if _, werr := target.Write(payload); werr != nil {
			break
		}
	}

	teardown()
	wg.Wait()
}

// pumpUDPTargetToWS reads datagrams from target and forwards each as one
// udprelaywire-framed WS BinaryMessage, until ctx is cancelled, target ends
// cleanly (io.EOF), or a real read/write error occurs. A read timeout
// (udpRelayIdleReadTimeout) is not fatal on its own: it exists so this
// goroutine notices ctx cancellation promptly instead of blocking on Read
// forever.
func pumpUDPTargetToWS(ctx context.Context, conn *websocket.Conn, target udpTarget) {
	buf := make([]byte, udpRelayReadBufferSize)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = target.SetReadDeadline(time.Now().Add(udpRelayIdleReadTimeout))
		n, err := target.Read(buf)
		if n > 0 {
			var frame bytes.Buffer
			if werr := udprelaywire.WriteFrame(&frame, buf[:n]); werr != nil {
				zap.L().Debug("udp relay: frame encode error", zap.Error(werr))
				return
			}
			if werr := conn.WriteMessage(websocket.BinaryMessage, frame.Bytes()); werr != nil {
				return
			}
		}
		if err == nil {
			continue
		}

		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			continue
		}
		if !errors.Is(err, io.EOF) {
			zap.L().Debug("udp relay: target read error", zap.Error(err))
		}
		return
	}
}
