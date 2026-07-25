package webserver

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
	"golang.org/x/crypto/ssh"
)

// Remote-forward multiplexing protocol.
//
// A single control WebSocket carries the whole reverse forward. After the
// hello/reply handshake (JSON TextMessages), every accepted connection on the
// remote (target) listener is multiplexed over that same WS using length-framed
// BinaryMessages, one frame per message:
//
//	byte 0..3 : connID (uint32, big-endian) — identifies the multiplexed conn
//	byte 4    : type (uint8) — rfFrameData | rfFrameClose | rfFrameOpen
//	byte 5..  : payload (data bytes; empty for open/close)
//
// Server → client: rfFrameOpen announces a freshly accepted conn; rfFrameData
// carries bytes read from the target conn; rfFrameClose signals it ended.
// Client → server: rfFrameData carries the local reply bytes for a connID;
// rfFrameClose signals the local side ended.
const (
	rfFrameData  byte = 0
	rfFrameClose byte = 1
	rfFrameOpen  byte = 2

	rfHeaderLen = 5 // connID uint32 (4) + type uint8 (1)
)

// WSRemoteForwardHello is the initial message expected on a remote-forward
// WebSocket connection. It requests that the server open a reverse listener on
// the target (remote) side and pipe accepted connections back to the client.
type WSRemoteForwardHello struct {
	SSHUser      string       `json:"ssh_user"`
	Record       hosts.Record `json:"record"`
	RemoteBind   string       `json:"remote_bind"`
	RemoteListen int          `json:"remote_listen"`
}

// handleWebRemoteForward proxies an SSH reverse (remote) port-forward over a
// control WebSocket. It makes the SERVER's SSH client listen ON THE TARGET and
// streams every accepted connection back to the calling client, which pipes it
// to a local target of its choosing.
//
// SECURITY: this opens a listener on the target side (reverse exposure) using
// the server's credentials. It is intentionally gated the same way as every
// other privileged endpoint — behind the /api/v1 authMiddleware (token + OPA)
// plus the explicit s.authorized(r) check below. Do not weaken either: a caller
// who reaches this handler can expose a port on the target host.
//
// @Summary Open reverse (remote) port-forward over WebSocket
// @Tags tunnels
// @Router /api/v1/ws/remote-forward [get]
// @Security BearerAuth
func (s *Server) handleWebRemoteForward(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
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

	var hello WSRemoteForwardHello
	if err := json.Unmarshal(helloRaw, &hello); err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"invalid hello json"}`))
		return
	}

	user := s.sshUser(hello.SSHUser)
	if !hello.Record.IsConnectable() {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"record is not connectable"}`))
		return
	}

	listenerFor := s.remoteListenerFor
	if listenerFor == nil {
		listenerFor = s.defaultRemoteListener
	}

	ln, cleanup, err := listenerFor(user, hello.Record, hello.RemoteBind, hello.RemoteListen)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	defer cleanup()
	defer ln.Close()

	reply, _ := json.Marshal(map[string]string{"status": "listening", "addr": ln.Addr().String()})
	if err := conn.WriteMessage(websocket.TextMessage, reply); err != nil {
		return
	}

	runRemoteForwardMux(r.Context(), conn, ln)
}

// defaultRemoteListener obtains a reverse listener on the target side by
// resolving the record's executor, dialing a leaf *ssh.Client and calling
// Listen on it. The returned cleanup releases the underlying HostClient.
func (s *Server) defaultRemoteListener(user string, r hosts.Record, bind string, port int) (net.Listener, func(), error) {
	if s.opts.ExecRegistry == nil {
		return nil, nil, fmt.Errorf("no exec registry configured")
	}
	executor := s.opts.ExecRegistry.ForRecord(r)
	if executor == nil {
		return nil, nil, fmt.Errorf("no executor found for record")
	}
	hc, err := executor.Dial(user, r)
	if err != nil {
		return nil, nil, fmt.Errorf("dial host: %w", err)
	}
	leafer, ok := hc.(interface{ LeafSSH() *ssh.Client })
	if !ok {
		_ = hc.Close()
		return nil, nil, fmt.Errorf("host client has no leaf ssh for remote forward")
	}
	leaf := leafer.LeafSSH()
	if leaf == nil {
		_ = hc.Close()
		return nil, nil, fmt.Errorf("ssh leaf client unavailable")
	}
	ln, err := leaf.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		_ = hc.Close()
		return nil, nil, fmt.Errorf("remote listen %s: %w", net.JoinHostPort(bind, strconv.Itoa(port)), err)
	}
	return ln, func() { _ = hc.Close() }, nil
}

// rfMux multiplexes accepted remote connections over a single control WS.
type rfMux struct {
	conn    *websocket.Conn
	writeMu sync.Mutex // gorilla allows at most one concurrent writer

	mu     sync.Mutex
	conns  map[uint32]net.Conn
	closed bool
}

func (m *rfMux) writeFrame(connID uint32, typ byte, payload []byte) error {
	frame := make([]byte, rfHeaderLen+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], connID)
	frame[4] = typ
	copy(frame[rfHeaderLen:], payload)

	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return m.conn.WriteMessage(websocket.BinaryMessage, frame)
}

// add registers c under id. It returns false (and does not register) if the mux
// is already shutting down, so the caller can close the orphaned conn.
func (m *rfMux) add(id uint32, c net.Conn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return false
	}
	m.conns[id] = c
	return true
}

func (m *rfMux) get(id uint32) (net.Conn, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	return c, ok
}

func (m *rfMux) remove(id uint32) (net.Conn, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.conns[id]
	if ok {
		delete(m.conns, id)
	}
	return c, ok
}

func (m *rfMux) closeConn(id uint32) {
	if c, ok := m.remove(id); ok {
		_ = c.Close()
	}
}

func (m *rfMux) writeToConn(id uint32, p []byte) {
	if c, ok := m.get(id); ok {
		_, _ = c.Write(p)
	}
}

// closeAll marks the mux closed and closes every tracked conn, unblocking their
// pumps. After this, add refuses new conns so the accept loop cannot leak one.
func (m *rfMux) closeAll() {
	m.mu.Lock()
	m.closed = true
	conns := m.conns
	m.conns = nil
	m.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// pumpToWS reads bytes from an accepted target conn and forwards them to the
// client as data frames, then signals close and cleans up when it ends.
func (m *rfMux) pumpToWS(id uint32, c net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			if werr := m.writeFrame(id, rfFrameData, buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	// Only announce close if we still own the id (teardown may have taken it).
	if _, existed := m.remove(id); existed {
		_ = m.writeFrame(id, rfFrameClose, nil)
	}
	_ = c.Close()
}

// runRemoteForwardMux drives the control WS until the client leaves or ctx is
// cancelled, then tears down the listener and every per-conn pump with no
// goroutine leaks.
func runRemoteForwardMux(ctx context.Context, conn *websocket.Conn, ln net.Listener) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	m := &rfMux{conn: conn, conns: make(map[uint32]net.Conn)}
	var wg sync.WaitGroup
	var nextID uint32

	teardown := sync.OnceFunc(func() {
		cancel()
		_ = ln.Close()
		m.closeAll()
	})
	defer teardown()

	// Cancel-driven teardown (server shutdown / request ctx done).
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		teardown()
	}()

	// Accept loop: each accepted target conn gets a fresh id, an open frame and
	// a pump goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			remote, err := ln.Accept()
			if err != nil {
				teardown()
				return
			}
			connID := atomic.AddUint32(&nextID, 1)
			if !m.add(connID, remote) {
				_ = remote.Close()
				return
			}
			if err := m.writeFrame(connID, rfFrameOpen, nil); err != nil {
				m.closeConn(connID)
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				m.pumpToWS(connID, remote)
			}()
		}
	}()

	// WS reader loop (client → server): reply bytes and closes.
	for {
		mt, p, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt != websocket.BinaryMessage || len(p) < rfHeaderLen {
			continue
		}
		connID := binary.BigEndian.Uint32(p[0:4])
		switch p[4] {
		case rfFrameData:
			m.writeToConn(connID, p[rfHeaderLen:])
		case rfFrameClose:
			m.closeConn(connID)
		default:
			zap.L().Debug("remote-forward: unknown frame type", zap.Uint8("type", p[4]))
		}
	}

	teardown()
	wg.Wait()
}
