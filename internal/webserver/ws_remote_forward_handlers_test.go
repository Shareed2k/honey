package webserver

import (
	"encoding/binary"
	"errors"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// fakeRemoteListener is an in-memory net.Listener whose accepted connections are
// fed by the test, so the remote-forward handler can be exercised without any
// real SSH client. Close() unblocks a pending Accept.
type fakeRemoteListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
	addr   net.Addr
}

func newFakeRemoteListener(addr net.Addr) *fakeRemoteListener {
	return &fakeRemoteListener{ch: make(chan net.Conn), closed: make(chan struct{}), addr: addr}
}

func (l *fakeRemoteListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, errors.New("listener closed")
	}
}

func (l *fakeRemoteListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *fakeRemoteListener) Addr() net.Addr { return l.addr }

func readRFFrame(t *testing.T, conn *websocket.Conn) (connID uint32, typ byte, payload []byte) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, p, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, mt)
	require.GreaterOrEqual(t, len(p), rfHeaderLen)
	return binary.BigEndian.Uint32(p[0:4]), p[4], p[rfHeaderLen:]
}

func writeRFFrame(t *testing.T, conn *websocket.Conn, connID uint32, typ byte, payload []byte) {
	t.Helper()
	frame := make([]byte, rfHeaderLen+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], connID)
	frame[4] = typ
	copy(frame[rfHeaderLen:], payload)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, frame))
}

func TestHandleWebRemoteForward(t *testing.T) {
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", DisableAuth: true, Version: "0"})
	require.NoError(t, err)

	fakeAddr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2222}
	ln := newFakeRemoteListener(fakeAddr)
	var cleanupCalled bool
	var cleanupMu sync.Mutex
	s.remoteListenerFor = func(_ string, _ hosts.Record, _ string, _ int) (net.Listener, func(), error) {
		return ln, func() {
			cleanupMu.Lock()
			cleanupCalled = true
			cleanupMu.Unlock()
		}, nil
	}

	ts := httptest.NewServer(s.router)
	defer ts.Close()

	// Snapshot baseline goroutines (NewServer + httptest) so goleak only checks
	// that the handler's per-request mux goroutines exit after the client leaves.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/api/v1/ws/remote-forward"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	hello := map[string]any{
		"ssh_user":      "ubuntu",
		"record":        hosts.Record{Provider: "gcp", PrimaryIP: "10.0.0.1"},
		"remote_bind":   "127.0.0.1",
		"remote_listen": 2222,
	}
	require.NoError(t, conn.WriteJSON(hello))

	var reply struct {
		Status string `json:"status"`
		Addr   string `json:"addr"`
		Error  string `json:"error"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	require.NoError(t, conn.ReadJSON(&reply))
	require.Empty(t, reply.Error)
	require.Equal(t, "listening", reply.Status)
	require.Equal(t, "127.0.0.1:2222", reply.Addr)

	// Simulate a connection arriving on the remote (target) side.
	accepted, remotePeer := net.Pipe()
	ln.ch <- accepted

	// The handler should announce the new conn with an open frame.
	openID, openType, _ := readRFFrame(t, conn)
	require.Equal(t, rfFrameOpen, openType)

	// Bytes written by the remote peer must arrive as data frames over the WS.
	go func() { _, _ = remotePeer.Write([]byte("from-remote")) }()
	dataID, dataType, payload := readRFFrame(t, conn)
	require.Equal(t, rfFrameData, dataType)
	require.Equal(t, openID, dataID)
	require.Equal(t, "from-remote", string(payload))

	// Bytes we send back over the WS must reach the accepted remote conn.
	writeRFFrame(t, conn, openID, rfFrameData, []byte("to-remote"))
	buf := make([]byte, len("to-remote"))
	_ = remotePeer.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = readFull(remotePeer, buf)
	require.NoError(t, err)
	require.Equal(t, "to-remote", string(buf))

	// Tear down: closing the client WS must stop the handler and release the leaf.
	_ = conn.Close()
	_ = remotePeer.Close()

	require.Eventually(t, func() bool {
		cleanupMu.Lock()
		defer cleanupMu.Unlock()
		return cleanupCalled
	}, 3*time.Second, 10*time.Millisecond, "cleanup (leaf release) must run on teardown")
}

func readFull(c net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := c.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
