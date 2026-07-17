package honeyprovider

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// upstreamDialer dials a remote address via the upstream Honey proxy,
// yielding a net.Conn that transports raw bytes to/from that address.
type upstreamDialer func(ctx context.Context, addr string) (net.Conn, error)

// upstreamHandshakeTimeout bounds the post-upgrade hello/reply exchange in
// dialUpstream so a WS upstream that accepts the upgrade but then stalls
// (never replying) cannot block the per-conn goroutine in listenAndPipe
// forever. Matches dialWS's HandshakeTimeout for consistency. A package var
// so tests can override it.
var upstreamHandshakeTimeout = 15 * time.Second

// dialUpstream dials a remote address via the upstream Honey proxy over
// /api/v1/ws/tunnel, bound to this client's ssh_user/record. It mirrors
// Executor.DialUpstream (exec.go) but is scoped to the Client so it can be
// used as an upstreamDialer for local/dynamic/remote forwards.
func (c *Client) dialUpstream(ctx context.Context, addr string) (net.Conn, error) {
	wsURL := strings.Replace(c.url, "http", "ws", 1) + "/api/v1/ws/tunnel"
	tlsCfg, err := clientTLSConfig(c.insecure, c.mtls, c.serverCA)
	if err != nil {
		return nil, err
	}
	token := c.token
	if c.mtls {
		token = ""
	}
	conn, err := dialWS(ctx, wsURL, token, tlsCfg, meshDialContext(c.mesh, c.meshAddr))
	if err != nil {
		return nil, err
	}

	hello := map[string]any{"ssh_user": c.user, "record": c.record, "target": addr}
	if err := conn.SetWriteDeadline(time.Now().Add(upstreamHandshakeTimeout)); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.WriteJSON(hello); err != nil {
		conn.Close()
		return nil, err
	}

	if err := conn.SetReadDeadline(time.Now().Add(upstreamHandshakeTimeout)); err != nil {
		conn.Close()
		return nil, err
	}
	var resp map[string]any
	if err := conn.ReadJSON(&resp); err != nil {
		conn.Close()
		return nil, err
	}
	if errStr, ok := resp["error"].(string); ok && errStr != "" {
		conn.Close()
		return nil, fmt.Errorf("upstream dial error: %s", errStr)
	}

	// Handshake complete: clear the deadlines so the long-lived data phase
	// (returned as a net.Conn) is not bounded by the handshake timeout.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}

	return &wsConn{conn: conn}, nil
}

// listenAndPipe starts a local TCP listener on bind:port and, for every
// accepted connection, resolves a target address via targetFor and dials it
// via dial, then pipes bytes bidirectionally between the local connection
// and the dialed upstream connection.
//
// Every goroutine it spawns exits via the returned stop() (idempotent: it
// cancels the internal ctx, closes the listener, and waits for in-flight
// goroutines) or when its connection naturally ends.
func listenAndPipe(ctx context.Context, bind string, port int, dial upstreamDialer,
	targetFor func(net.Conn) (string, error),
) (string, int, func(), error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return "", 0, nil, fmt.Errorf("honey upstream forward listen: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			local, err := ln.Accept()
			if err != nil {
				return // listener closed by stop()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer local.Close()
				target, err := targetFor(local)
				if err != nil {
					return
				}
				up, err := dial(ctx, target)
				if err != nil {
					return
				}
				defer up.Close()
				pipe(ctx, local, up)
			}()
		}
	}()
	stop := sync.OnceFunc(func() { cancel(); ln.Close(); wg.Wait() })
	ta := ln.Addr().(*net.TCPAddr)
	return ta.IP.String(), ta.Port, stop, nil
}

// pipe copies both directions and returns when either side ends or ctx cancels.
func pipe(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	_ = a.Close()
	_ = b.Close()
}

// StartLocalForward starts a local TCP listener that forwards every accepted
// connection to remoteHost:remotePort via the upstream Honey proxy.
func (c *Client) StartLocalForward(ctx context.Context, bind string, localPort int, remoteHost string, remotePort int) (string, int, func(), error) {
	target := net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))
	return listenAndPipe(ctx, bind, localPort, c.dialer(), func(net.Conn) (string, error) {
		return target, nil
	})
}

// dialer returns the upstream dial seam to use: dialUpstreamFn if the Client
// was constructed with one set (the normal path, via Executor.Dial, and the
// path tests use to inject a fake), else c.dialUpstream directly as a
// defensive fallback for Clients built without it.
func (c *Client) dialer() upstreamDialer {
	if c.dialUpstreamFn != nil {
		return c.dialUpstreamFn
	}
	return c.dialUpstream
}
