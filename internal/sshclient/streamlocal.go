package sshclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// streamLocalOpenMsg is the type-specific payload for an OpenSSH
// "direct-streamlocal@openssh.com" channel open: the remote unix socket path
// followed by two reserved fields the protocol requires to be empty/zero.
type streamLocalOpenMsg struct {
	SocketPath string
	Reserved0  string
	Reserved1  uint32
}

// chanConn adapts an ssh.Channel to net.Conn. SSH channels carry no addresses
// or deadlines; the forward loop only Reads/Writes/Closes (and CloseWrite via
// closeWrite), so the address methods return a synthetic unix addr and the
// deadline methods report unsupported (mirroring crypto/ssh's own channel-conn).
type chanConn struct {
	ssh.Channel
	socket string
}

type unixName struct{ name string }

func (unixName) Network() string  { return "unix" }
func (u unixName) String() string { return u.name }

func (c *chanConn) LocalAddr() net.Addr              { return unixName{c.socket} }
func (c *chanConn) RemoteAddr() net.Addr             { return unixName{c.socket} }
func (c *chanConn) SetDeadline(time.Time) error      { return errStreamLocalDeadline }
func (c *chanConn) SetReadDeadline(time.Time) error  { return errStreamLocalDeadline }
func (c *chanConn) SetWriteDeadline(time.Time) error { return errStreamLocalDeadline }

var errStreamLocalDeadline = errors.New("ssh: streamlocal deadline not supported")

// maxUnixSocketPathLen is the smaller of the darwin (104) and linux (108)
// sun_path limits; a local socket path at or beyond it cannot be bound.
const maxUnixSocketPathLen = 104

var _ net.Conn = (*chanConn)(nil)

// DialStreamLocal opens a direct-streamlocal channel to a remote unix socket and
// returns it as a net.Conn. This is the codebase's only raw ssh.OpenChannel:
// x/crypto/ssh's Client.Dial supports "tcp" only, so there is no high-level
// helper for unix sockets. The remote sshd must permit StreamLocalForwarding.
func DialStreamLocal(client *ssh.Client, remoteSocket string) (net.Conn, error) {
	if client == nil {
		return nil, errors.New("ssh: nil client for streamlocal dial")
	}
	if !filepath.IsAbs(remoteSocket) {
		return nil, fmt.Errorf("ssh: remote socket must be absolute: %q", remoteSocket)
	}
	payload := ssh.Marshal(streamLocalOpenMsg{SocketPath: remoteSocket})
	ch, reqs, err := client.OpenChannel("direct-streamlocal@openssh.com", payload)
	if err != nil {
		return nil, fmt.Errorf("ssh: open streamlocal %s: %w", remoteSocket, err)
	}
	go ssh.DiscardRequests(reqs)
	return &chanConn{Channel: ch, socket: remoteSocket}, nil
}

// StartLocalSocketForward listens on a local unix socket and forwards each
// accepted connection to remoteSocket over a direct-streamlocal channel. stop
// closes the listener and removes the local socket file. Both paths must be
// absolute; localSocket should live in an operator-owned directory.
func StartLocalSocketForward(ctx context.Context, client *ssh.Client, localSocket, remoteSocket string) (string, func(), error) {
	if client == nil {
		return "", nil, errors.New("ssh: nil client for streamlocal forward")
	}
	if !filepath.IsAbs(localSocket) {
		return "", nil, fmt.Errorf("ssh: local socket must be absolute: %q", localSocket)
	}
	if !filepath.IsAbs(remoteSocket) {
		return "", nil, fmt.Errorf("ssh: remote socket must be absolute: %q", remoteSocket)
	}
	// The kernel caps a unix socket path (sun_path) at ~104 bytes on darwin
	// (108 on linux); use the smaller so a too-long path fails with a clear
	// message instead of a cryptic "bind: invalid argument".
	if len(localSocket) >= maxUnixSocketPathLen {
		return "", nil, fmt.Errorf("ssh: local socket path too long (%d >= %d bytes): %q", len(localSocket), maxUnixSocketPathLen, localSocket)
	}
	_ = os.Remove(localSocket) // clear a stale socket file; benign if absent
	ln, err := net.Listen("unix", localSocket)
	if err != nil {
		return "", nil, fmt.Errorf("ssh: listen unix %s: %w", localSocket, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			_ = ln.Close()
			_ = os.Remove(localSocket)
		})
	}
	go func() {
		<-runCtx.Done()
		_ = ln.Close()
	}()
	go acceptForwardLoop(runCtx, ln, func() (net.Conn, error) {
		return DialStreamLocal(client, remoteSocket)
	})
	return localSocket, stop, nil
}
