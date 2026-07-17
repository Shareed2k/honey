package honeyprovider

import (
	"context"
	"fmt"
	"net"
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
// Every goroutine it spawns exits via the returned stop() (idempotent: it
// cancels the internal ctx, closes the UDP listener, waits for the read
// loop and every per-flow goroutine to return, then best-effort kills the
// remote socat process).
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

	relayPort, killRemote := c.startRemoteSocatUDPRelay(remoteHost, remotePort)
	relayAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort))

	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		udpRelayLoop(runCtx, udpLn, &wg, c.dialer(), relayAddr)
	}()

	stop = sync.OnceFunc(func() {
		cancel()
		_ = udpLn.Close()
		wg.Wait()
		killRemote()
	})
	return host, port, stop, nil
}

// startRemoteSocatUDPRelay starts a remote socat TCP<->UDP relay on the
// target via c.Run (which the server executes over its own SSH connection
// to the target), listening on a pseudo-random high port and forwarding to
// remoteHost:remotePort. c.Run blocks for the lifetime of the remote
// command, so it is started in a background goroutine; the returned kill
// func best-effort terminates it via a follow-up c.Run(pkill) call.
//
// Mirrors sshclient.startRemoteSocatUDPRelay's port-pick and socat command
// form, adapted to the Honey proxy's synchronous exec seam (c.Run) in place
// of a long-lived SSH session that can simply be closed to kill the child.
func (c *Client) startRemoteSocatUDPRelay(remoteHost string, remotePort int) (relayPort int, kill func()) {
	relayPort = 20000 + int(time.Now().UnixNano()%20000)
	cmd := socatUDPRelayCmd(relayPort, remoteHost, remotePort)

	go func() {
		// Best-effort: runs for as long as the remote socat process is
		// alive. Errors (e.g. socat missing, port unavailable) are not
		// surfaced synchronously to the caller -- the first UDP flow will
		// simply fail to dial the relay. kill() below is how callers tear
		// this down.
		_, _ = c.Run(cmd)
	}()

	kill = sync.OnceFunc(func() {
		_, _ = c.Run(socatKillCmd(relayPort))
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
