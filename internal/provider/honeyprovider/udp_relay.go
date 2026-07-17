package honeyprovider

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// udpRelayIdleTimeout bounds how long a per-flow upstream TCP connection
// stays open without receiving a reply before it is torn down. Mirrors
// sshclient.udpRelayLoop's idle timeout.
const udpRelayIdleTimeout = 30 * time.Second

// StartUDPRelay bridges a local UDP listener to remoteHost:remotePort via
// the upstream Honey proxy.
//
// DialUpstream (c.dialer()/c.dialUpstream) only carries a TCP byte stream,
// so UDP datagrams cannot be forwarded to it directly. Instead, this starts
// a remote "socat TCP-LISTEN ... UDP:remoteHost:remotePort" relay on the
// target (via c.Run, which the server executes over its own SSH connection
// to the target) and bridges local UDP flows to that relay over the
// upstream TCP dialer. useSocat must therefore be true; SOCKS-UDP-ASSOCIATE
// (dynamic_forward.go) is a separate, unrelated path.
//
// Every goroutine it spawns -- the read loop, every per-flow goroutine, and
// the goroutine that launches the remote socat relay via c.Run -- exits via
// the returned stop() (idempotent: it cancels the internal ctx, closes the
// UDP listener, best-effort kills the remote socat process, then waits for
// every one of those goroutines to return). killRemote() must run before
// wg.Wait(): it is what makes the remote socat process exit, which in turn
// unblocks the foreground c.Run(socatCmd) HTTP call so its goroutine can
// return.
func (c *Client) StartUDPRelay(ctx context.Context, bind string, localPort int, remoteHost string, remotePort int, useSocat bool) (host string, port int, stop func(), err error) {
	if !useSocat {
		return "", 0, nil, fmt.Errorf("honey upstream UDP relay requires socat mode (direct UDP is not supported over the proxy)")
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
