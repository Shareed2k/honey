package webserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/udprelaywire"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// fakeUDPTarget is a udpTarget test double that echoes every Write back as
// the next Read, so the handler can be exercised without a real UDP socket.
// Close unblocks a pending/future Read immediately (mirroring *net.UDPConn),
// which is what lets teardown finish promptly instead of waiting out
// SetReadDeadline.
type fakeUDPTarget struct {
	ch        chan []byte
	closed    chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	deadline time.Time
}

func newFakeUDPTarget() *fakeUDPTarget {
	return &fakeUDPTarget{ch: make(chan []byte, 16), closed: make(chan struct{})}
}

func (f *fakeUDPTarget) Write(p []byte) (int, error) {
	cp := append([]byte(nil), p...)
	select {
	case f.ch <- cp:
		return len(p), nil
	case <-f.closed:
		return 0, net.ErrClosed
	}
}

func (f *fakeUDPTarget) Read(p []byte) (int, error) {
	f.mu.Lock()
	deadline := f.deadline
	f.mu.Unlock()

	var timeoutC <-chan time.Time
	if !deadline.IsZero() {
		d := time.Until(deadline)
		if d <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		timeoutC = timer.C
	}

	select {
	case b := <-f.ch:
		return copy(p, b), nil
	case <-f.closed:
		return 0, io.EOF
	case <-timeoutC:
		return 0, os.ErrDeadlineExceeded
	}
}

func (f *fakeUDPTarget) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeUDPTarget) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	f.deadline = t
	f.mu.Unlock()
	return nil
}

var _ udpTarget = (*fakeUDPTarget)(nil)

// fakeUDPDialer is a udpDialer test double: it records the target it was
// asked to dial and returns either a canned udpTarget or a canned error, so
// tests can assert ValidateTarget rejected an input before dialing (the
// dialer must never be called) or that a dial error surfaces to the client.
type fakeUDPDialer struct {
	target  udpTarget
	dialErr error

	mu           sync.Mutex
	called       bool
	dialedTarget string
}

func (f *fakeUDPDialer) DialUDP(_ context.Context, target string) (udpTarget, error) {
	f.mu.Lock()
	f.called = true
	f.dialedTarget = target
	f.mu.Unlock()
	if f.dialErr != nil {
		return nil, f.dialErr
	}
	return f.target, nil
}

func (f *fakeUDPDialer) wasCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called
}

var _ udpDialer = (*fakeUDPDialer)(nil)

func TestHandleWebUDPRelay(t *testing.T) {
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", DisableAuth: true, Version: "0"})
	require.NoError(t, err)

	target := newFakeUDPTarget()
	fd := &fakeUDPDialer{target: target}
	s.udpDialer = fd

	ts := httptest.NewServer(s.router)
	defer ts.Close()

	// Snapshot baseline goroutines (NewServer + httptest) so goleak only
	// checks that the handler's per-connection goroutines exit after the
	// client leaves.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/api/v1/ws/udp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.WriteJSON(map[string]string{"target": "127.0.0.1:9999"}))

	var reply struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	require.NoError(t, conn.ReadJSON(&reply))
	require.Empty(t, reply.Error)
	require.Equal(t, "connected", reply.Status)
	require.True(t, fd.wasCalled())
	require.Equal(t, "127.0.0.1:9999", fd.dialedTarget)

	// One udprelaywire-framed datagram per WS BinaryMessage, in each
	// direction: send a framed "ping" and expect the fake target's echo
	// back, still framed, in a single BinaryMessage.
	var frame bytes.Buffer
	require.NoError(t, udprelaywire.WriteFrame(&frame, []byte("ping")))
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, frame.Bytes()))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, p, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, mt)
	payload, err := udprelaywire.ReadFrame(bytes.NewReader(p))
	require.NoError(t, err)
	require.Equal(t, "ping", string(payload))

	require.NoError(t, conn.Close())
}

func TestHandleWebUDPRelay_InvalidTarget(t *testing.T) {
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", DisableAuth: true, Version: "0"})
	require.NoError(t, err)

	fd := &fakeUDPDialer{target: newFakeUDPTarget()}
	s.udpDialer = fd

	ts := httptest.NewServer(s.router)
	defer ts.Close()

	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/api/v1/ws/udp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Host contains a shell metacharacter: ValidateTarget must reject it
	// before the dialer is ever invoked (SSRF-shaped surface guard).
	require.NoError(t, conn.WriteJSON(map[string]string{"target": "bad;host:1234"}))

	var reply struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	require.NoError(t, conn.ReadJSON(&reply))
	require.NotEmpty(t, reply.Error)
	require.False(t, fd.wasCalled(), "dialer must not be called for an invalid target")
}

func TestHandleWebUDPRelay_OPADenied(t *testing.T) {
	const src = `package honey
import rego.v1
default allow := false
default deny_reason := "no udp relay for you"
allow if input.action == "api_request"`
	enf, err := policy.NewFromSource(context.Background(), "deny.rego", src)
	require.NoError(t, err)

	s := newTestServer(t, Options{Enforcer: enf})

	fd := &fakeUDPDialer{target: newFakeUDPTarget()}
	s.udpDialer = fd

	ts := httptest.NewServer(s.router)
	defer ts.Close()

	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/api/v1/ws/udp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(map[string]string{"target": "1.2.3.4:53"}))

	var reply struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	require.NoError(t, conn.ReadJSON(&reply))
	require.NotEmpty(t, reply.Error)
	require.False(t, fd.wasCalled(), "dialer must not be called when OPA denies the target")
}

func TestHandleWebUDPRelay_DialError(t *testing.T) {
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", DisableAuth: true, Version: "0"})
	require.NoError(t, err)

	fd := &fakeUDPDialer{dialErr: errors.New("connection refused")}
	s.udpDialer = fd

	ts := httptest.NewServer(s.router)
	defer ts.Close()

	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/api/v1/ws/udp"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(map[string]string{"target": "127.0.0.1:9999"}))

	var reply struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	require.NoError(t, conn.ReadJSON(&reply))
	require.NotEmpty(t, reply.Error)
	require.True(t, fd.wasCalled())
}
