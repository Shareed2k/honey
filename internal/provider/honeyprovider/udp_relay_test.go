package honeyprovider

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/udprelaywire"
)

// fakeUDPStreamOpener is a udpStreamOpener test double: Open records the
// target it was asked to open and returns one end of a net.Pipe, with the
// other end driven by an in-test goroutine that reads one udprelaywire frame
// at a time and writes it straight back (framed), standing in for the real
// server's /api/v1/ws/udp echo-shaped bridge without any network I/O.
type fakeUDPStreamOpener struct {
	mu     sync.Mutex
	called bool
	target string
}

func (f *fakeUDPStreamOpener) Open(_ context.Context, target string) (net.Conn, error) {
	f.mu.Lock()
	f.called = true
	f.target = target
	f.mu.Unlock()

	client, server := net.Pipe()
	go func() {
		defer server.Close()
		for {
			payload, err := udprelaywire.ReadFrame(server)
			if err != nil {
				return
			}
			var frame bytes.Buffer
			if werr := udprelaywire.WriteFrame(&frame, payload); werr != nil {
				return
			}
			if _, werr := server.Write(frame.Bytes()); werr != nil {
				return
			}
		}
	}()
	return client, nil
}

func (f *fakeUDPStreamOpener) wasCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called
}

func (f *fakeUDPStreamOpener) dialedTarget() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.target
}

var _ udpStreamOpener = (*fakeUDPStreamOpener)(nil)

// TestStartUDPRelay_ServerBridge covers StartUDPRelay's useSocat=false,
// server-vantage mode end to end (minus the real network): a fake
// udpStreamOpener stands in for the real WS dial to /api/v1/ws/udp, and a
// real client UDP datagram round-trips through startServerBridgeUDPRelay's
// framing (WriteFrame -> single Write; ReadFrame -> WriteToUDP) to prove the
// bridge itself is wired correctly, independent of the real opener.
func TestStartUDPRelay_ServerBridge(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))

	opener := &fakeUDPStreamOpener{}
	c := &Client{udpStreamOpener: opener}

	host, port, stop, err := c.StartUDPRelay(context.Background(), "127.0.0.1", 0, "target-host", 53, false)
	require.NoError(t, err)
	require.NotNil(t, stop)

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(host), Port: port})
	require.NoError(t, err)
	defer client.Close()

	_, err = client.Write([]byte("ping"))
	require.NoError(t, err)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
	buf := make([]byte, 4)
	n, err := client.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf[:n]))

	require.True(t, opener.wasCalled())
	require.Equal(t, "target-host:53", opener.dialedTarget())

	// stop() must be idempotent.
	stop()
	stop()
}

// TestStartUDPRelay_ServerBridge_ValidatesTarget covers the SSRF-shaped-
// surface guard on the server-bridge path: remoteHost:remotePort must be
// validated by udprelaywire.ValidateTarget before anything is opened, so a
// remoteHost containing shell/URL metacharacters is rejected before the
// opener (and thus the server) ever sees it.
func TestStartUDPRelay_ServerBridge_ValidatesTarget(t *testing.T) {
	opener := &fakeUDPStreamOpener{}
	c := &Client{udpStreamOpener: opener}

	host, port, stop, err := c.StartUDPRelay(context.Background(), "127.0.0.1", 0, "bad;host", 53, false)
	require.Error(t, err)
	require.Empty(t, host)
	require.Zero(t, port)
	require.Nil(t, stop)
	require.False(t, opener.wasCalled(), "opener must not be called for an invalid target")
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

// TestStartUDPRelay_SocatGlue drives the full Client.StartUDPRelay entry
// point (not just udpRelayLoop directly, as TestUDPRelayLoop does) with the
// runFn/dialUpstreamFn seams faked out, so the useSocat=true glue --
// startRemoteSocatUDPRelay building/running the real socat command string,
// and udpRelayLoop dialing the relay it stood up -- is exercised without a
// real socat binary or a real Honey upstream.
//
// The fake runFn models c.Run's real behavior closely enough to prove
// Finding 1's fix: the "start socat" call blocks (as the real foreground
// c.Run(socatCmd) HTTP call would for as long as the remote socat process is
// alive) until the "kill" call (the follow-up c.Run(pkillCmd) stop() makes)
// unblocks it. If stop() still waited before killing (the pre-fix order),
// this test would hang/deadlock instead of returning promptly.
func TestStartUDPRelay_SocatGlue(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))

	// Fake "remote socat relay": a loopback TCP listener that echoes
	// whatever it receives, standing in for the real socat TCP-LISTEN that
	// startRemoteSocatUDPRelay would otherwise start on the actual remote
	// host via c.Run.
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer echoLn.Close()

	go func() {
		for {
			conn, aerr := echoLn.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
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

	var mu sync.Mutex
	var calls []string
	killed := make(chan struct{})
	runFn := func(cmd string) ([]byte, error) {
		mu.Lock()
		calls = append(calls, cmd)
		mu.Unlock()
		if strings.Contains(cmd, "pkill") {
			close(killed)
			return []byte("ok"), nil
		}
		// Models c.Run's real, blocking-until-the-remote-process-exits
		// behavior for the socat start command: block until killed by the
		// pkill call above.
		<-killed
		return []byte("ok"), nil
	}
	dialUpstreamFn := func(_ context.Context, _ string) (net.Conn, error) {
		// Ignore addr (the pseudo-random relay port StartUDPRelay picked
		// internally) and dial the fake relay directly -- proving
		// udpRelayLoop's dial call is reached and wired to this seam.
		return net.Dial("tcp", echoLn.Addr().String())
	}

	c := &Client{runFn: runFn, dialUpstreamFn: dialUpstreamFn}

	// Finding 2: a remoteHost containing shell metacharacters must be
	// rejected before any socat command is built or goroutine started.
	badHost, badPort, badStop, err := c.StartUDPRelay(context.Background(), "127.0.0.1", 0, "bad;host", 53, true)
	require.Error(t, err)
	require.Empty(t, badHost)
	require.Zero(t, badPort)
	require.Nil(t, badStop)
	mu.Lock()
	require.Empty(t, calls, "invalid remoteHost must not reach runFn")
	mu.Unlock()

	host, port, stop, err := c.StartUDPRelay(context.Background(), "127.0.0.1", 0, "10.0.0.5", 53, true)
	require.NoError(t, err)
	require.NotNil(t, stop)

	client, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: net.ParseIP(host), Port: port})
	require.NoError(t, err)
	defer client.Close()

	_, err = client.Write([]byte("ping"))
	require.NoError(t, err)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
	buf := make([]byte, 4)
	n, err := client.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf[:n]))

	// Idempotency (Finding 1): calling stop() twice must not panic, hang, or
	// run the kill command a second time.
	stop()
	stop()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, calls, 2, "expected exactly one socat-start call and one kill call")
	var startCmd, killCmd string
	for _, cmd := range calls {
		if strings.Contains(cmd, "pkill") {
			killCmd = cmd
		} else {
			startCmd = cmd
		}
	}
	require.Regexp(t, `^socat TCP-LISTEN:\d+,bind=127\.0\.0\.1,reuseaddr,fork UDP:10\.0\.0\.5:53$`, startCmd)
	require.Regexp(t, `^pkill -f 'TCP-LISTEN:\d+,bind=127\.0\.0\.1'$`, killCmd)
}
