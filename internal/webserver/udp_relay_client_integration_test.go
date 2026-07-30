package webserver

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
)

// TestUDPRelay_ClientServer_Integration is a drift guard: it runs the REAL
// client-side server-bridge opener (honeyprovider.Client.StartUDPRelay with
// useSocat=false, which dials the real WS transport and hands frames to/from
// internal/udprelaywire) against the REAL server-side handler
// (handleWebUDPRelay, exercised through the real *Server router via
// httptest) with only the UDP target itself faked out (fakeUDPDialer /
// fakeUDPTarget, the same test doubles ws_udp_relay_handlers_test.go uses).
//
// Task 1 (internal/udprelaywire) shipped one shared codec; Task 2 shipped
// the server endpoint against it; this task wires the client to it. Each
// side's own unit tests exercise the codec correctly in isolation, but nothing
// short of a real client dial against a real server proves the two sides
// actually agree on the wire: one frame per WS BinaryMessage, in both
// directions. That is exactly what this test round-trips end to end.
func TestUDPRelay_ClientServer_Integration(t *testing.T) {
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", DisableAuth: true, Version: "0"})
	require.NoError(t, err)

	target := newFakeUDPTarget()
	fd := &fakeUDPDialer{target: target}
	s.forwardingAPI.udpDialer = fd

	ts := httptest.NewServer(s.router)
	defer ts.Close()

	// Snapshot baseline goroutines (NewServer + httptest), matching the
	// convention ws_udp_relay_handlers_test.go's own tests use, so goleak
	// only checks that this test's own client/server goroutines exit after
	// stop().
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	exec := &honeyprovider.Executor{URL: ts.URL}
	hc, err := exec.Dial("test-user", hosts.Record{Name: "test-host"})
	require.NoError(t, err)
	defer hc.Close()

	host, port, stop, err := hc.StartUDPRelay(context.Background(), "127.0.0.1", 0, "127.0.0.1", 9999, false)
	require.NoError(t, err)
	require.NotNil(t, stop)
	defer stop()

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(host), Port: port})
	require.NoError(t, err)
	defer client.Close()

	// The server-bridge dials the per-flow stream lazily, on the first
	// datagram from this client -- so fd.wasCalled()/dialedTarget() are only
	// meaningful after this round trip completes.
	_, err = client.Write([]byte("ping"))
	require.NoError(t, err)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 4)
	n, err := client.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf[:n]))

	require.True(t, fd.wasCalled())
	require.Equal(t, "127.0.0.1:9999", fd.dialedTarget)

	stop()
}
