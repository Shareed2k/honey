package honeyprovider

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Remote-forward multiplexing protocol (client side). Mirrors the frame format
// documented in internal/webserver/ws_remote_forward_handlers.go: one WS
// BinaryMessage per frame, a 5-byte header [connID uint32 big-endian][type
// uint8] followed by the payload.
//
//	server → client: rfFrameClientOpen announces an accepted remote conn,
//	                 rfFrameClientData carries its bytes, rfFrameClientClose ends it.
//	client → server: rfFrameClientData carries the local reply bytes for a connID,
//	                 rfFrameClientClose signals the local side ended.
const (
	rfFrameClientData  byte = 0
	rfFrameClientClose byte = 1
	rfFrameClientOpen  byte = 2

	rfClientHeaderLen = 5
)

// StartRemoteForward opens a reverse (remote) port-forward via the upstream
// Honey proxy: it asks the server to listen on remoteBind:remoteListen on the
// target side and pipes every connection accepted there to localHost:localTarget
// on this machine. It returns the address the server bound, an idempotent stop,
// and an error. Every goroutine it starts exits on stop() (which closes the
// control WS, cancels ctx and waits) — no leaks.
func (c *Client) StartRemoteForward(ctx context.Context, remoteBind string, remoteListen int, localHost string, localTarget int) (string, func(), error) {
	wsURL := strings.Replace(c.url, "http", "ws", 1) + "/api/v1/ws/remote-forward"
	tlsCfg, err := clientTLSConfig(c.insecure, c.mtls, c.serverCA)
	if err != nil {
		return "", nil, err
	}
	token := c.token
	if c.mtls {
		token = ""
	}

	ctx, cancel := context.WithCancel(ctx)
	conn, err := dialWS(ctx, wsURL, token, tlsCfg, meshDialContext(c.mesh, c.meshAddr))
	if err != nil {
		cancel()
		return "", nil, err
	}

	addr, err := remoteForwardHandshake(conn, c.user, c.record, remoteBind, remoteListen)
	if err != nil {
		cancel()
		_ = conn.Close()
		return "", nil, err
	}

	m := &rfClientMux{
		conn:   conn,
		conns:  make(map[uint32]net.Conn),
		ctx:    ctx,
		target: net.JoinHostPort(localHost, strconv.Itoa(localTarget)),
	}
	var wg sync.WaitGroup
	m.wg = &wg

	wg.Add(1)
	go func() {
		defer wg.Done()
		m.readLoop()
	}()

	stop := sync.OnceFunc(func() {
		cancel()
		_ = conn.Close()
		m.closeAll()
		wg.Wait()
	})

	return addr, stop, nil
}

// remoteForwardHandshake sends the hello and reads the listening reply, bounded
// by a deadline so a server that upgrades but never replies cannot hang the
// caller (mirrors dialUpstream). It clears the deadlines before returning so the
// long-lived data phase is not bounded by the handshake timeout.
func remoteForwardHandshake(conn *websocket.Conn, user string, record any, remoteBind string, remoteListen int) (string, error) {
	hello := map[string]any{
		"ssh_user":      user,
		"record":        record,
		"remote_bind":   remoteBind,
		"remote_listen": remoteListen,
	}
	if err := conn.SetWriteDeadline(time.Now().Add(upstreamHandshakeTimeout)); err != nil {
		return "", err
	}
	if err := conn.WriteJSON(hello); err != nil {
		return "", err
	}
	if err := conn.SetReadDeadline(time.Now().Add(upstreamHandshakeTimeout)); err != nil {
		return "", err
	}
	var resp struct {
		Status string `json:"status"`
		Addr   string `json:"addr"`
		Error  string `json:"error"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("remote forward error: %s", resp.Error)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return "", err
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		return "", err
	}
	return resp.Addr, nil
}

// rfClientMux demultiplexes the control WS into per-connID local dials.
type rfClientMux struct {
	conn    *websocket.Conn
	writeMu sync.Mutex // gorilla allows at most one concurrent writer

	mu     sync.Mutex
	conns  map[uint32]net.Conn
	closed bool

	ctx    context.Context
	target string
	wg     *sync.WaitGroup
}

func (m *rfClientMux) writeFrame(connID uint32, typ byte, payload []byte) error {
	frame := make([]byte, rfClientHeaderLen+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], connID)
	frame[4] = typ
	copy(frame[rfClientHeaderLen:], payload)

	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return m.conn.WriteMessage(websocket.BinaryMessage, frame)
}

func (m *rfClientMux) add(id uint32, c net.Conn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.conns[id] = c
	return true
}

func (m *rfClientMux) get(id uint32) (net.Conn, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	return c, ok
}

func (m *rfClientMux) remove(id uint32) (net.Conn, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	if ok {
		delete(m.conns, id)
	}
	return c, ok
}

func (m *rfClientMux) closeConn(id uint32) {
	if c, ok := m.remove(id); ok {
		_ = c.Close()
	}
}

func (m *rfClientMux) writeToConn(id uint32, p []byte) {
	if c, ok := m.get(id); ok {
		_, _ = c.Write(p)
	}
}

func (m *rfClientMux) closeAll() {
	m.mu.Lock()
	m.closed = true
	conns := m.conns
	m.conns = nil
	m.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// handleOpen dials the local target for a newly announced remote conn and starts
// pumping its reply bytes back to the server. Dialing synchronously in readLoop
// guarantees the conn is registered before any data frame for it is processed.
func (m *rfClientMux) handleOpen(id uint32) {
	local, err := (&net.Dialer{}).DialContext(m.ctx, "tcp", m.target)
	if err != nil {
		_ = m.writeFrame(id, rfFrameClientClose, nil)
		return
	}
	if !m.add(id, local) {
		_ = local.Close()
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.pumpToWS(id, local)
	}()
}

// pumpToWS reads bytes from a local conn and forwards them to the server as data
// frames, then signals close and cleans up when it ends.
func (m *rfClientMux) pumpToWS(id uint32, c net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			if werr := m.writeFrame(id, rfFrameClientData, buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	if _, existed := m.remove(id); existed {
		_ = m.writeFrame(id, rfFrameClientClose, nil)
	}
	_ = c.Close()
}

// readLoop demultiplexes server → client frames until the WS closes, then closes
// every local conn so their pumps exit.
func (m *rfClientMux) readLoop() {
	for {
		mt, p, err := m.conn.ReadMessage()
		if err != nil {
			break
		}
		if mt != websocket.BinaryMessage || len(p) < rfClientHeaderLen {
			continue
		}
		connID := binary.BigEndian.Uint32(p[0:4])
		switch p[4] {
		case rfFrameClientOpen:
			m.handleOpen(connID)
		case rfFrameClientData:
			m.writeToConn(connID, p[rfClientHeaderLen:])
		case rfFrameClientClose:
			m.closeConn(connID)
		}
	}
	m.closeAll()
}
