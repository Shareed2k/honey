package honeyprovider

import (
	"context"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"golang.org/x/net/proxy"
)

// TestStartDynamicForward drives the local SOCKS5 proxy with a real SOCKS5
// client (golang.org/x/net/proxy.SOCKS5) and a fake dialUpstreamFn — no real
// server/mesh involved. The fake dials a real loopback TCP echo server
// instead of a net.Pipe: go-socks5's Proxy loop relies on *net.TCPConn
// half-close (CloseWrite) propagation to unblock both copy directions when
// the SOCKS client closes, so the "upstream" target must be a real TCP
// connection for the whole chain to tear down without leaking goroutines.
func TestStartDynamicForward(t *testing.T) {
	// GlobalTunnelPool.sweepLoop is a process-lifetime background goroutine
	// unrelated to this test (see the identical ignore in TestMain above).
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))

	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLn.Close()

	var echoWG sync.WaitGroup
	go func() {
		for {
			conn, aerr := echoLn.Accept()
			if aerr != nil {
				return
			}
			echoWG.Add(1)
			go func(c net.Conn) {
				defer echoWG.Done()
				defer c.Close()
				_, _ = io.Copy(c, c) // echo until the peer half-closes
			}(conn)
		}
	}()

	addrCh := make(chan string, 1)
	fake := func(_ context.Context, addr string) (net.Conn, error) {
		addrCh <- addr
		return net.Dial("tcp", echoLn.Addr().String())
	}

	c := &Client{dialUpstreamFn: fake}
	host, port, stop, err := c.StartDynamicForward(context.Background(), "127.0.0.1", 0)
	require.NoError(t, err)

	dialer, err := proxy.SOCKS5("tcp", net.JoinHostPort(host, strconv.Itoa(port)), nil, proxy.Direct)
	require.NoError(t, err)

	conn, err := dialer.Dial("tcp", "example:1234")
	require.NoError(t, err)

	select {
	case got := <-addrCh:
		require.Equal(t, "example:1234", got)
	case <-time.After(2 * time.Second):
		t.Fatal("fake dialUpstreamFn was not invoked")
	}

	_, err = conn.Write([]byte("ping"))
	require.NoError(t, err)
	buf := make([]byte, 4)
	_, err = io.ReadFull(conn, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))

	require.NoError(t, conn.Close())

	// Wait for the echo handler goroutine to observe the half-close and
	// exit, so stop() below has nothing left in flight.
	waitDone := make(chan struct{})
	go func() {
		echoWG.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("echo handler did not exit after client close")
	}

	stop()
	stop() // idempotent
}
