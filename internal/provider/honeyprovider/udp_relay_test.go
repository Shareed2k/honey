package honeyprovider

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestStartUDPRelay_SocatRequired covers the design constraint documented on
// Client.StartUDPRelay: DialUpstream only carries TCP streams, so bridging
// UDP requires the remote socat TCP<->UDP hop. useSocat=false must fail
// fast with a clear error instead of silently doing nothing.
func TestStartUDPRelay_SocatRequired(t *testing.T) {
	c := &Client{}
	host, port, stop, err := c.StartUDPRelay(context.Background(), "127.0.0.1", 0, "target", 53, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "socat")
	require.Empty(t, host)
	require.Zero(t, port)
	require.Nil(t, stop)
}

// TestUDPRelayLoop drives the core UDP<->TCP bridging logic directly (the
// important logic per the task brief), with a fake upstream dialer that
// dials a real loopback TCP echo listener instead of a real socat/mesh
// upstream. It sends a datagram to the local UDP listener and expects the
// echoed reply, then verifies stop()'s cancellation tears down every
// goroutine cleanly (checked by TestMain's goleak.VerifyTestMain).
func TestUDPRelayLoop(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))

	// Fake "remote socat relay": a loopback TCP listener that echoes
	// whatever it receives, standing in for the real socat TCP-LISTEN that
	// forwards to a UDP target on the actual remote host.
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
				buf := make([]byte, 4096)
				for {
					n, rerr := c.Read(buf)
					if n > 0 {
						_, _ = c.Write(buf[:n])
					}
					if rerr != nil {
						return
					}
				}
			}(conn)
		}
	}()

	udpLn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	require.NoError(t, err)

	dial := func(_ context.Context, addr string) (net.Conn, error) {
		require.Equal(t, echoLn.Addr().String(), addr)
		return net.Dial("tcp", addr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		udpRelayLoop(ctx, udpLn, &wg, dial, echoLn.Addr().String())
	}()

	client, err := net.DialUDP("udp", nil, udpLn.LocalAddr().(*net.UDPAddr))
	require.NoError(t, err)
	defer client.Close()

	_, err = client.Write([]byte("ping"))
	require.NoError(t, err)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
	buf := make([]byte, 4)
	n, err := client.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf[:n]))

	// stop: cancel + close the UDP listener + wait for every spawned
	// goroutine (the read loop and the per-flow bridge) to exit.
	cancel()
	require.NoError(t, udpLn.Close())
	wg.Wait()
}

// socatUDPRelayCmd / socatKillCmd are exercised directly (not over the
// network) per the brief's guidance to verify "the command string it WOULD
// run" without standing up a real target or Client.Run over HTTP.
func TestSocatUDPRelayCmd(t *testing.T) {
	got := socatUDPRelayCmd(24000, "10.0.0.5", 53)
	require.Equal(t, "socat TCP-LISTEN:24000,bind=127.0.0.1,reuseaddr,fork UDP:10.0.0.5:53", got)
}

func TestSocatKillCmd(t *testing.T) {
	got := socatKillCmd(24000)
	require.Contains(t, got, "24000")
	require.Contains(t, got, "pkill")
}
