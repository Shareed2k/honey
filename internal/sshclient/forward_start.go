package sshclient

import (
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

// StartLocalForward listens locally and dials remoteHost:remotePort via client.
func StartLocalForward(ctx context.Context, client *ssh.Client, bindHost string, localPort int, remoteHost string, remotePort int) (listenHost string, listenPort int, stop func(), err error) {
	if client == nil {
		return "", 0, nil, fmt.Errorf("nil ssh client")
	}
	if localPort < 0 || localPort >= 65536 {
		return "", 0, nil, fmt.Errorf("local port out of range: %d", localPort)
	}
	if remotePort <= 0 || remotePort >= 65536 {
		return "", 0, nil, fmt.Errorf("remote port out of range: %d", remotePort)
	}
	bindHost = strings.TrimSpace(bindHost)
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	remoteHost = strings.TrimSpace(remoteHost)
	if remoteHost == "" {
		return "", 0, nil, fmt.Errorf("empty remote host")
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(localPort)))
	if err != nil {
		return "", 0, nil, fmt.Errorf("listen %s:%d: %w", bindHost, localPort, err)
	}
	addr := ln.Addr().String()
	host, portStr, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		_ = ln.Close()
		return "", 0, nil, splitErr
	}
	port, _ := strconv.Atoi(portStr)

	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stopFn := func() {
		once.Do(func() {
			cancel()
			_ = ln.Close()
		})
	}
	go func() {
		<-runCtx.Done()
		_ = ln.Close()
	}()

	remoteAddr := net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))
	go acceptForwardLoop(runCtx, ln, func() (net.Conn, error) {
		return client.Dial("tcp", remoteAddr)
	})

	return host, port, stopFn, nil
}

// StartRemoteForward listens on the remote side and dials localHost:localPort locally.
func StartRemoteForward(ctx context.Context, client *ssh.Client, remoteBind string, remoteListenPort int, localHost string, localPort int) (remoteAddr string, stop func(), err error) {
	if client == nil {
		return "", nil, fmt.Errorf("nil ssh client")
	}
	if remoteListenPort <= 0 || remoteListenPort >= 65536 {
		return "", nil, fmt.Errorf("remote listen port out of range: %d", remoteListenPort)
	}
	if localPort <= 0 || localPort >= 65536 {
		return "", nil, fmt.Errorf("local port out of range: %d", localPort)
	}
	remoteBind = strings.TrimSpace(remoteBind)
	if remoteBind == "" {
		remoteBind = "127.0.0.1"
	}
	localHost = strings.TrimSpace(localHost)
	if localHost == "" {
		localHost = "127.0.0.1"
	}

	ln, err := client.Listen("tcp", net.JoinHostPort(remoteBind, strconv.Itoa(remoteListenPort)))
	if err != nil {
		return "", nil, fmt.Errorf("remote listen %s:%d: %w", remoteBind, remoteListenPort, err)
	}
	remoteAddr = ln.Addr().String()

	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stopFn := func() {
		once.Do(func() {
			cancel()
			_ = ln.Close()
		})
	}
	go func() {
		<-runCtx.Done()
		_ = ln.Close()
	}()

	localAddr := net.JoinHostPort(localHost, strconv.Itoa(localPort))
	go acceptForwardLoop(runCtx, ln, func() (net.Conn, error) {
		return net.Dial("tcp", localAddr)
	})

	return remoteAddr, stopFn, nil
}

// StartDynamicForward starts a minimal SOCKS5 CONNECT proxy on bindHost:localPort via client.
func StartDynamicForward(ctx context.Context, client *ssh.Client, bindHost string, localPort int) (socksHost string, socksPort int, stop func(), err error) {
	if client == nil {
		return "", 0, nil, fmt.Errorf("nil ssh client")
	}
	if localPort < 0 || localPort >= 65536 {
		return "", 0, nil, fmt.Errorf("local port out of range: %d", localPort)
	}
	bindHost = strings.TrimSpace(bindHost)
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, strconv.Itoa(localPort)))
	if err != nil {
		return "", 0, nil, fmt.Errorf("listen %s:%d: %w", bindHost, localPort, err)
	}
	addr := ln.Addr().String()
	host, portStr, splitErr := net.SplitHostPort(addr)
	if splitErr != nil {
		_ = ln.Close()
		return "", 0, nil, splitErr
	}
	port, _ := strconv.Atoi(portStr)

	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stopFn := func() {
		once.Do(func() {
			cancel()
			_ = ln.Close()
		})
	}
	go func() {
		<-runCtx.Done()
		_ = ln.Close()
	}()

	go acceptDynamicSOCKS5Loop(runCtx, ln, client)
	return host, port, stopFn, nil
}

// StartUDPRelay bridges a local UDP listener to a remote UDP target via SSH.
// When remoteSocat is true, a remote socat TCP listener relays to the UDP target.
func StartUDPRelay(ctx context.Context, client *ssh.Client, bindHost string, localPort int, remoteHost string, remotePort int, remoteSocat bool) (listenHost string, listenPort int, stop func(), err error) {
	if client == nil {
		return "", 0, nil, fmt.Errorf("nil ssh client")
	}
	if localPort < 0 || localPort >= 65536 {
		return "", 0, nil, fmt.Errorf("local port out of range: %d", localPort)
	}
	if remotePort <= 0 || remotePort >= 65536 {
		return "", 0, nil, fmt.Errorf("remote port out of range: %d", remotePort)
	}
	bindHost = strings.TrimSpace(bindHost)
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	remoteHost = strings.TrimSpace(remoteHost)
	if remoteHost == "" {
		return "", 0, nil, fmt.Errorf("empty remote host")
	}

	udpAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(bindHost, strconv.Itoa(localPort)))
	if err != nil {
		return "", 0, nil, err
	}
	udpLn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return "", 0, nil, fmt.Errorf("listen udp %s:%d: %w", bindHost, localPort, err)
	}
	host, portStr, splitErr := net.SplitHostPort(udpLn.LocalAddr().String())
	if splitErr != nil {
		_ = udpLn.Close()
		return "", 0, nil, splitErr
	}
	port, _ := strconv.Atoi(portStr)

	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stopFn := func() {
		once.Do(func() {
			cancel()
			_ = udpLn.Close()
		})
	}

	var remoteTCP string
	if remoteSocat {
		relayPort, relayStop, socatErr := startRemoteSocatUDPRelay(ctx, client, remoteHost, remotePort)
		if socatErr != nil {
			_ = udpLn.Close()
			return "", 0, nil, socatErr
		}
		prevStop := stopFn
		stopFn = func() {
			prevStop()
			relayStop()
		}
		remoteTCP = net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort))
	} else {
		remoteTCP = net.JoinHostPort(remoteHost, strconv.Itoa(remotePort))
	}

	go udpRelayLoop(runCtx, udpLn, func() (net.Conn, error) {
		return client.Dial("tcp", remoteTCP)
	})

	return host, port, stopFn, nil
}

// StartTunForward starts an OpenSSH tunnel device forward (ssh -w local:remote -N).
func StartTunForward(ctx context.Context, user, hostAlias string, sshPort, tunLocal, tunRemote int) (tunName string, stop func(), err error) {
	hostAlias = strings.TrimSpace(hostAlias)
	if hostAlias == "" {
		return "", nil, fmt.Errorf("empty host alias")
	}
	if tunLocal < 0 || tunRemote < 0 {
		return "", nil, fmt.Errorf("invalid tun units local=%d remote=%d", tunLocal, tunRemote)
	}

	dest := sshGDestination(user, hostAlias)
	args := []string{
		"-w", fmt.Sprintf("%d:%d", tunLocal, tunRemote),
		"-N",
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
	}
	if sshPort > 0 && sshPort < 65536 {
		args = append(args, "-p", strconv.Itoa(sshPort))
	}
	args = append(args, dest)

	// #nosec G204 -- fixed ssh binary; args are structured flags and destination.
	cmd := exec.CommandContext(ctx, "ssh", args...)
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("ssh -w: %w", err)
	}

	tunName = fmt.Sprintf("tun%d", tunLocal)
	var once sync.Once
	stopFn := func() {
		once.Do(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				done := make(chan struct{})
				go func() {
					_ = cmd.Wait()
					close(done)
				}()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					_ = cmd.Process.Kill()
				}
			}
		})
	}

	go func() {
		<-ctx.Done()
		stopFn()
	}()

	return tunName, stopFn, nil
}

func acceptForwardLoop(ctx context.Context, ln net.Listener, dial func() (net.Conn, error)) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go func(local net.Conn) {
			defer func() { _ = local.Close() }()
			remote, derr := dial()
			if derr != nil {
				return
			}
			defer func() { _ = remote.Close() }()
			bridgeConns(local, remote)
		}(conn)
	}
}

func bridgeConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		_ = closeWrite(b)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		_ = closeWrite(a)
	}()
	wg.Wait()
}

func closeWrite(c net.Conn) error {
	if tc, ok := c.(*net.TCPConn); ok {
		return tc.CloseWrite()
	}
	return nil
}

func acceptDynamicSOCKS5Loop(ctx context.Context, ln net.Listener, client *ssh.Client) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		go handleSOCKS5Connect(ctx, conn, client)
	}
}

func handleSOCKS5Connect(_ context.Context, conn net.Conn, client *ssh.Client) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	buf := make([]byte, 257)
	n, err := io.ReadAtLeast(conn, buf, 2)
	if err != nil || n < 2 || buf[0] != 0x05 {
		return
	}
	nmethods := int(buf[1])
	if n < 2+nmethods {
		return
	}
	if _, err = conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}
	n, err = io.ReadAtLeast(conn, buf[:7], 7)
	if err != nil || n < 7 || buf[0] != 0x05 || buf[1] != 0x01 {
		return
	}
	atyp := buf[3]
	var host string
	var idx int
	switch atyp {
	case 0x01:
		if n < 10 {
			return
		}
		host = net.IP(buf[4:8]).String()
		idx = 8
	case 0x03:
		hostLen := int(buf[4])
		if n < 5+hostLen+2 {
			return
		}
		host = string(buf[5 : 5+hostLen])
		idx = 5 + hostLen
	case 0x04:
		if n < 22 {
			return
		}
		host = net.IP(buf[4:20]).String()
		idx = 20
	default:
		return
	}
	port := int(buf[idx])<<8 | int(buf[idx+1])
	_ = conn.SetDeadline(time.Time{})

	target := net.JoinHostPort(host, strconv.Itoa(port))
	remote, err := client.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer func() { _ = remote.Close() }()
	_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	bridgeConns(conn, remote)
}

func startRemoteSocatUDPRelay(ctx context.Context, client *ssh.Client, remoteHost string, remotePort int) (relayPort int, stop func(), err error) {
	relayPort = 20000 + int(time.Now().UnixNano()%20000)

	sess, err := client.NewSession()
	if err != nil {
		return 0, nil, err
	}
	cmd := fmt.Sprintf("socat TCP-LISTEN:%d,bind=127.0.0.1,reuseaddr,fork UDP:%s:%d", relayPort, remoteHost, remotePort)
	sess.Stdout = io.Discard
	sess.Stderr = io.Discard
	if err := sess.Start(cmd); err != nil {
		_ = sess.Close()
		return 0, nil, fmt.Errorf("start remote socat: %w", err)
	}

	var once sync.Once
	stopFn := func() {
		once.Do(func() {
			_ = sess.Close()
		})
	}
	go func() {
		<-ctx.Done()
		stopFn()
	}()
	return relayPort, stopFn, nil
}

func udpRelayLoop(ctx context.Context, ln *net.UDPConn, dial func() (net.Conn, error)) {
	buf := make([]byte, 65535)
	type flowKey struct {
		client string
	}
	flows := make(map[flowKey]net.Conn)
	var mu sync.Mutex

	for {
		_ = ln.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, addr, err := ln.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return
		}
		key := flowKey{client: addr.String()}
		mu.Lock()
		tcp, ok := flows[key]
		if !ok {
			tcp, err = dial()
			if err != nil {
				mu.Unlock()
				continue
			}
			flows[key] = tcp
			go func(c net.Conn, a *net.UDPAddr) {
				defer func() {
					mu.Lock()
					delete(flows, key)
					mu.Unlock()
					_ = c.Close()
				}()
				rbuf := make([]byte, 65535)
				for {
					_ = c.SetReadDeadline(time.Now().Add(30 * time.Second))
					rn, rerr := c.Read(rbuf)
					if rerr != nil {
						return
					}
					_, _ = ln.WriteToUDP(rbuf[:rn], a)
				}
			}(tcp, addr)
		}
		mu.Unlock()
		_, _ = tcp.Write(buf[:n])
	}
}
