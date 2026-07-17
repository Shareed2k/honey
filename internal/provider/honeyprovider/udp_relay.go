package honeyprovider

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/udprelaywire"
)

// udpRelayIdleTimeout bounds how long a per-flow upstream TCP connection
// stays open without receiving a reply before it is torn down. Mirrors
// sshclient.udpRelayLoop's idle timeout.
const udpRelayIdleTimeout = 30 * time.Second

// StartUDPRelay bridges a local UDP listener to remoteHost:remotePort via
// the upstream Honey proxy, in one of two modes selected by useSocat:
//
//   - useSocat=true (target-vantage): DialUpstream (c.dialer()/c.dialUpstream)
//     only carries a TCP byte stream, so UDP datagrams cannot be forwarded to
//     it directly. Instead, this starts a remote "socat TCP-LISTEN ...
//     UDP:remoteHost:remotePort" relay on the target (via c.Run, which the
//     server executes over its own SSH connection to the target) and bridges
//     local UDP flows to that relay over the upstream TCP dialer. This
//     requires socat to be installed on the target.
//   - useSocat=false (server-vantage): bridges local UDP flows to the
//     server's /api/v1/ws/udp endpoint (see startServerBridgeUDPRelay below),
//     which dials remoteHost:remotePort itself. No tooling is required on the
//     target; the server reaches whatever it itself can route to.
//
// SOCKS-UDP-ASSOCIATE (dynamic_forward.go) is a separate, unrelated path.
//
// Every goroutine either mode spawns exits via the returned stop()
// (idempotent, no leaks); see startRemoteSocatUDPRelay and
// startServerBridgeUDPRelay for the per-mode teardown details.
func (c *Client) StartUDPRelay(ctx context.Context, bind string, localPort int, remoteHost string, remotePort int, useSocat bool) (host string, port int, stop func(), err error) {
	if !useSocat {
		return c.startServerBridgeUDPRelay(ctx, bind, localPort, remoteHost, remotePort)
	}
	if localPort < 0 || localPort >= 65536 {
		return "", 0, nil, fmt.Errorf("local port out of range: %d", localPort)
	}
	if remotePort <= 0 || remotePort >= 65536 {
		return "", 0, nil, fmt.Errorf("remote port out of range: %d", remotePort)
	}
	bind = strings.TrimSpace(bind)
	if bind == "" {
		bind = "127.0.0.1"
	}
	remoteHost = strings.TrimSpace(remoteHost)
	if remoteHost == "" {
		return "", 0, nil, fmt.Errorf("empty remote host")
	}
	// remoteHost is interpolated unescaped into a shell command run on the
	// target (socatUDPRelayCmd/socatKillCmd), so it must be validated as a
	// plain IP literal or DNS hostname before it ever reaches c.Run.
	if err := validateRemoteHost(remoteHost); err != nil {
		return "", 0, nil, err
	}

	udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(bind, strconv.Itoa(localPort)))
	if err != nil {
		return "", 0, nil, err
	}
	udpLn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return "", 0, nil, fmt.Errorf("honey upstream UDP relay listen: %w", err)
	}
	host, portStr, splitErr := net.SplitHostPort(udpLn.LocalAddr().String())
	if splitErr != nil {
		_ = udpLn.Close()
		return "", 0, nil, splitErr
	}
	port, _ = strconv.Atoi(portStr)

	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup

	relayPort, killRemote := c.startRemoteSocatUDPRelay(&wg, remoteHost, remotePort)
	relayAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort))

	wg.Add(1)
	go func() {
		defer wg.Done()
		udpRelayLoop(runCtx, udpLn, &wg, c.dialer(), relayAddr)
	}()

	stop = sync.OnceFunc(func() {
		cancel()
		_ = udpLn.Close()
		// killRemote before wg.Wait: it makes the remote socat process
		// exit, which unblocks the foreground c.Run(socatCmd) HTTP call
		// started in startRemoteSocatUDPRelay's goroutine so it can
		// return and wg.Wait() below can complete.
		killRemote()
		wg.Wait()
	})
	return host, port, stop, nil
}

// udpStreamOpener opens a framed byte-stream to the server's /api/v1/ws/udp
// endpoint for a UDP target. Production dials the WS over mesh/mTLS/token
// (realUDPStreamOpener); tests supply a net.Pipe-backed fake.
type udpStreamOpener interface {
	Open(ctx context.Context, target string) (net.Conn, error)
}

// realUDPStreamOpener is the production udpStreamOpener: it dials the
// server's /api/v1/ws/udp endpoint over the same WS/mesh/mTLS/token
// transport as c.dialUpstream (see forward_proxy.go's dialUpstream, which
// this mirrors), sends the hello {"target": target}, and waits for
// {"status":"connected"} (surfacing a {"error":...} reply as an error)
// before handing back the wsConn as a net.Conn carrying the framed datagram
// stream.
type realUDPStreamOpener struct {
	c *Client
}

var _ udpStreamOpener = (*realUDPStreamOpener)(nil)

func (o *realUDPStreamOpener) Open(ctx context.Context, target string) (net.Conn, error) {
	c := o.c
	wsURL := strings.Replace(c.url, "http", "ws", 1) + "/api/v1/ws/udp"
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

	hello := map[string]any{"target": target}
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
		return nil, fmt.Errorf("udp relay dial error: %s", errStr)
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

// udpOpener returns the UDP-relay stream-open seam to use: udpStreamOpener
// if the Client was constructed with one set (the normal path, via
// Executor.Dial, and the path tests use to inject a fake), else a real
// opener built from this Client as a defensive fallback for Clients built
// without it. Mirrors c.dialer()/c.runner().
func (c *Client) udpOpener() udpStreamOpener {
	if c.udpStreamOpener != nil {
		return c.udpStreamOpener
	}
	return &realUDPStreamOpener{c: c}
}

// startServerBridgeUDPRelay implements StartUDPRelay's useSocat=false,
// server-vantage mode: it bridges a local UDP listener to remoteHost:
// remotePort via the server's /api/v1/ws/udp endpoint, which dials
// remoteHost:remotePort itself (server-vantage, not target- or
// client-vantage) -- so, unlike startRemoteSocatUDPRelay, no tooling of any
// kind needs to be present on the target; the server reaches whatever it
// itself can route to (including hosts only the server has network access
// to, e.g. via a server-side VPN or mesh).
//
// remoteHost:remotePort is validated with udprelaywire.ValidateTarget before
// anything is opened (the local UDP listener or any per-flow stream) since
// it is echoed to the server as the hello target.
//
// Every goroutine this starts -- the UDP read loop and one reader goroutine
// per client source-address flow -- exits via the returned stop()
// (idempotent: cancel + close the UDP listener, which in turn makes the read
// loop close every still-open per-flow stream before it returns, then
// wg.Wait()).
func (c *Client) startServerBridgeUDPRelay(ctx context.Context, bind string, localPort int, remoteHost string, remotePort int) (host string, port int, stop func(), err error) {
	if localPort < 0 || localPort >= 65536 {
		return "", 0, nil, fmt.Errorf("local port out of range: %d", localPort)
	}
	remoteHost = strings.TrimSpace(remoteHost)
	target := net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))
	if err := udprelaywire.ValidateTarget(target); err != nil {
		return "", 0, nil, err
	}

	bind = strings.TrimSpace(bind)
	if bind == "" {
		bind = "127.0.0.1"
	}

	udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(bind, strconv.Itoa(localPort)))
	if err != nil {
		return "", 0, nil, err
	}
	udpLn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return "", 0, nil, fmt.Errorf("honey upstream UDP relay listen: %w", err)
	}
	host, portStr, splitErr := net.SplitHostPort(udpLn.LocalAddr().String())
	if splitErr != nil {
		_ = udpLn.Close()
		return "", 0, nil, splitErr
	}
	port, _ = strconv.Atoi(portStr)

	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		udpServerBridgeLoop(runCtx, udpLn, &wg, c.udpOpener(), target)
	}()

	stop = sync.OnceFunc(func() {
		cancel()
		_ = udpLn.Close()
		wg.Wait()
	})
	return host, port, stop, nil
}

// udpServerBridgeLoop reads datagrams from ln and bridges each distinct
// client source address to its own framed stream, opened via one
// opener.Open(ctx, target) call per flow (keyed by client address). Each
// local datagram is written as a single framed message: WriteFrame first
// encodes it into a buffer, then the whole frame is sent with one Write call
// (this preserves the MESSAGE-BOUNDARY invariant the server relies on -- the
// production stream is a *wsConn, whose Write maps one call to one WS
// BinaryMessage, so the frame must be fully built before Write is called,
// never split across two Writes). A dedicated goroutine per flow reads
// framed replies back via udprelaywire.ReadFrame and relays each to the
// originating client address; it returns (ending the flow) on any read
// error, including a clean io.EOF at a frame boundary, so a per-flow idle
// timeout (udpRelayIdleTimeout, set as the read deadline before each
// ReadFrame call) naturally tears down an inactive flow.
//
// It runs until ctx is cancelled or ln is closed, at which point it closes
// any still-open flow streams so their reader goroutines (tracked via wg,
// along with the caller's own wg.Add(1) for this loop) unblock and exit
// promptly instead of waiting out the idle timeout.
func udpServerBridgeLoop(ctx context.Context, ln *net.UDPConn, wg *sync.WaitGroup, opener udpStreamOpener, target string) {
	buf := make([]byte, 65535)
	flows := make(map[string]net.Conn)
	var mu sync.Mutex

	for {
		_ = ln.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := ln.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}

		key := addr.String()
		mu.Lock()
		stream, ok := flows[key]
		if !ok {
			stream, err = opener.Open(ctx, target)
			if err != nil {
				mu.Unlock()
				zap.L().Debug("udp relay: open server-bridge stream failed", zap.String("target", target), zap.Error(err))
				continue
			}
			flows[key] = stream
			wg.Add(1)
			go func(s net.Conn, clientAddr *net.UDPAddr, flowKey string) {
				defer wg.Done()
				defer func() {
					mu.Lock()
					delete(flows, flowKey)
					mu.Unlock()
					_ = s.Close()
				}()
				for {
					_ = s.SetReadDeadline(time.Now().Add(udpRelayIdleTimeout))
					payload, rerr := udprelaywire.ReadFrame(s)
					if rerr != nil {
						// Covers both a clean end (io.EOF at a frame
						// boundary) and any other error (idle-timeout,
						// truncation, closed stream): either way this flow
						// is done, and ReadFrame never returns a partial
						// payload alongside a non-nil error, so there is
						// nothing left to relay.
						return
					}
					_, _ = ln.WriteToUDP(payload, clientAddr)
				}
			}(stream, addr, key)
		}
		mu.Unlock()

		var frame bytes.Buffer
		if werr := udprelaywire.WriteFrame(&frame, buf[:n]); werr != nil {
			continue
		}
		_, _ = stream.Write(frame.Bytes())
	}

	mu.Lock()
	for _, s := range flows {
		_ = s.Close()
	}
	mu.Unlock()
}

// validateRemoteHost rejects remoteHost values that are not a plain IP
// literal or DNS hostname. remoteHost is interpolated unescaped into a
// shell command executed on the target via c.Run (socatUDPRelayCmd /
// socatKillCmd), so anything containing shell metacharacters must be
// rejected here rather than reaching the remote shell.
func validateRemoteHost(remoteHost string) error {
	if net.ParseIP(remoteHost) != nil {
		return nil
	}
	if !hostnamePattern.MatchString(remoteHost) || len(remoteHost) > 253 {
		return fmt.Errorf("invalid remote host %q: must be an IP address or a DNS hostname (letters, digits, '.', '-' only)", remoteHost)
	}
	return nil
}

// hostnamePattern matches a syntactically valid DNS hostname: dot-separated
// labels of letters, digits and hyphens, each 1-63 characters, neither
// starting nor ending with a hyphen. It rejects any shell metacharacters
// (spaces, ;, |, &, $, backticks, etc.), which is the property that matters
// here since remoteHost is interpolated unescaped into a remote shell
// command.
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$`)

// runner returns the command-run seam to use: runFn if the Client was
// constructed with one set (the normal path, via Executor.Dial, and the
// path tests use to inject a fake), else c.Run directly as a defensive
// fallback for Clients built without it. Mirrors c.dialer() in
// forward_proxy.go.
func (c *Client) runner() func(cmd string) ([]byte, error) {
	if c.runFn != nil {
		return c.runFn
	}
	return c.Run
}

// startRemoteSocatUDPRelay starts a remote socat TCP<->UDP relay on the
// target via c.runner() (c.runFn if set, else c.Run -- which the server
// executes over its own SSH connection to the target), listening on a
// pseudo-random high port and forwarding to remoteHost:remotePort. The run
// call blocks for the lifetime of the remote command, so it is started in a
// background goroutine tracked on wg (the same WaitGroup StartUDPRelay's
// stop() waits on); the returned kill func best-effort terminates it via a
// follow-up run(pkill) call, which is what makes that goroutine's run(cmd)
// call return.
//
// Mirrors sshclient.startRemoteSocatUDPRelay's port-pick and socat command
// form, adapted to the Honey proxy's synchronous exec seam (c.Run) in place
// of a long-lived SSH session that can simply be closed to kill the child.
func (c *Client) startRemoteSocatUDPRelay(wg *sync.WaitGroup, remoteHost string, remotePort int) (relayPort int, kill func()) {
	relayPort = 20000 + int(time.Now().UnixNano()%20000)
	cmd := socatUDPRelayCmd(relayPort, remoteHost, remotePort)
	run := c.runner()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// Best-effort: runs for as long as the remote socat process is
		// alive. Errors (e.g. socat missing, port unavailable) are not
		// surfaced synchronously to the caller -- the first UDP flow will
		// simply fail to dial the relay. kill() below is how callers tear
		// this down, which is also what unblocks and retires this
		// goroutine so StartUDPRelay's stop() can return.
		_, _ = run(cmd)
	}()

	kill = sync.OnceFunc(func() {
		_, _ = run(socatKillCmd(relayPort))
	})
	return relayPort, kill
}

// socatUDPRelayCmd builds the exact remote socat invocation used to start
// the TCP<->UDP relay, matching sshclient.startRemoteSocatUDPRelay's
// command form.
func socatUDPRelayCmd(relayPort int, remoteHost string, remotePort int) string {
	return fmt.Sprintf("socat TCP-LISTEN:%d,bind=127.0.0.1,reuseaddr,fork UDP:%s:%d", relayPort, remoteHost, remotePort)
}

// socatKillCmd builds a best-effort remote command to terminate the socat
// relay started by socatUDPRelayCmd, matched by its listen port.
func socatKillCmd(relayPort int) string {
	return fmt.Sprintf("pkill -f 'TCP-LISTEN:%d,bind=127.0.0.1'", relayPort)
}

// udpRelayLoop reads datagrams from ln and bridges each distinct client
// address to its own upstream TCP connection (opened via dial to
// relayAddr), copying replies back to the originating client. One
// connection is opened per flow (keyed by client address) and torn down
// after udpRelayIdleTimeout of inactivity.
//
// It runs until ctx is canceled or ln is closed, at which point it closes
// any still-open flow connections so their goroutines (tracked via wg, along
// with the caller's own wg.Add(1) for this loop) unblock and exit promptly
// instead of waiting out the idle timeout.
func udpRelayLoop(ctx context.Context, ln *net.UDPConn, wg *sync.WaitGroup, dial upstreamDialer, relayAddr string) {
	buf := make([]byte, 65535)
	flows := make(map[string]net.Conn)
	var mu sync.Mutex

	for {
		_ = ln.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := ln.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			break
		}

		key := addr.String()
		mu.Lock()
		conn, ok := flows[key]
		if !ok {
			conn, err = dial(ctx, relayAddr)
			if err != nil {
				mu.Unlock()
				continue
			}
			flows[key] = conn
			wg.Add(1)
			go func(c net.Conn, clientAddr *net.UDPAddr, flowKey string) {
				defer wg.Done()
				defer func() {
					mu.Lock()
					delete(flows, flowKey)
					mu.Unlock()
					_ = c.Close()
				}()
				rbuf := make([]byte, 65535)
				for {
					_ = c.SetReadDeadline(time.Now().Add(udpRelayIdleTimeout))
					rn, rerr := c.Read(rbuf)
					if rn > 0 {
						_, _ = ln.WriteToUDP(rbuf[:rn], clientAddr)
					}
					if rerr != nil {
						return
					}
				}
			}(conn, addr, key)
		}
		mu.Unlock()
		_, _ = conn.Write(buf[:n])
	}

	mu.Lock()
	for _, conn := range flows {
		_ = conn.Close()
	}
	mu.Unlock()
}
