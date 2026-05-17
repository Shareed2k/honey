package dockerprovider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/cuetry"
)

// dialHoneyTransportConn dials the remote Docker Engine endpoint over an existing SSH client.
func dialHoneyTransportConn(ctx context.Context, sshClient *ssh.Client, dialP DialParams, runAs string) (net.Conn, error) {
	runAs = strings.TrimSpace(runAs)
	if runAs == "" {
		return sshClient.DialContext(ctx, dialP.Network, dialP.Address)
	}
	if dialP.Network != "unix" {
		return nil, fmt.Errorf("docker run_as %q is only supported for unix sockets over SSH (got network %q)", runAs, dialP.Network)
	}
	if err := cuetry.ValidateRunAsUser(runAs); err != nil {
		return nil, err
	}
	return dialUnixSocketViaRunAs(ctx, sshClient, dialP.Address, runAs)
}

func dialUnixSocketViaRunAs(ctx context.Context, sshClient *ssh.Client, socketPath, runAs string) (net.Conn, error) {
	session, err := sshClient.NewSession()
	if err != nil {
		return nil, fmt.Errorf("ssh session: %w", err)
	}
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		_ = session.Close()
		return nil, fmt.Errorf("empty docker socket path")
	}
	cmd := runAsProxyCommand(runAs, socketPath)
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	if err := session.Start(cmd); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf(
			"start docker socket proxy (run_as=%s): %w (need passwordless sudo and docker system dial-stdio on the remote host)",
			runAs, err,
		)
	}
	conn := &sshSessionConn{
		ctx:     ctx,
		session: session,
		stdin:   stdin,
		stdout:  stdout,
		stderr:  stderr,
		runAs:   runAs,
	}
	conn.startStderrDrain()
	return conn, nil
}

// runAsProxyCommand builds a remote command that proxies the Docker API over stdio as runAs.
func runAsProxyCommand(runAs, socketPath string) string {
	u := escapeSingleQuoted(runAs)
	inner := fmt.Sprintf("exec docker -H %s system dial-stdio", shellSingleQuote(dockerHostFlag(socketPath)))
	return fmt.Sprintf(`sudo -n -u '%s' -- sh -lc %s`, u, shellSingleQuote(inner))
}

func dockerHostFlag(socketPath string) string {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return "unix:///var/run/docker.sock"
	}
	if strings.HasPrefix(socketPath, "unix://") {
		return socketPath
	}
	if strings.HasPrefix(socketPath, "/") {
		return "unix://" + socketPath
	}
	return "unix://" + socketPath
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'\''`) + "'"
}

func escapeSingleQuoted(s string) string {
	return strings.ReplaceAll(s, `'`, `'\''`)
}

// sshSessionConn bridges an SSH session running docker system dial-stdio to net.Conn for the Moby client.
type sshSessionConn struct {
	ctx     context.Context
	session *ssh.Session
	stdin   io.WriteCloser
	stdout  io.Reader
	stderr  io.Reader
	runAs   string

	closeOnce  sync.Once
	closeErr   error
	stderrBuf  bytes.Buffer
	stderrDone chan struct{}
	stderrOnce sync.Once
}

func (c *sshSessionConn) startStderrDrain() {
	c.stderrOnce.Do(func() {
		c.stderrDone = make(chan struct{})
		go func() {
			defer close(c.stderrDone)
			_, _ = io.Copy(&c.stderrBuf, c.stderr)
		}()
	})
}

func (c *sshSessionConn) stderrSnapshot() string {
	if c.stderrDone != nil {
		select {
		case <-c.stderrDone:
		case <-time.After(200 * time.Millisecond):
		}
	}
	return strings.TrimSpace(c.stderrBuf.String())
}

func (c *sshSessionConn) Read(b []byte) (int, error) {
	if c.ctx != nil {
		select {
		case <-c.ctx.Done():
			return 0, c.ctx.Err()
		default:
		}
	}
	n, err := c.stdout.Read(b)
	if err != nil && err != io.EOF {
		return n, err
	}
	if err == io.EOF {
		if msg := c.stderrSnapshot(); msg != "" {
			return n, fmt.Errorf("docker socket proxy (run_as=%s): %s", c.runAs, msg)
		}
	}
	return n, err
}

func (c *sshSessionConn) Write(b []byte) (int, error) {
	if c.ctx != nil {
		select {
		case <-c.ctx.Done():
			return 0, c.ctx.Err()
		default:
		}
	}
	return c.stdin.Write(b)
}

func (c *sshSessionConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.stdin.Close()
		waitDone := make(chan error, 1)
		go func() { waitDone <- c.session.Wait() }()
		select {
		case err := <-waitDone:
			if err != nil {
				if msg := c.stderrSnapshot(); msg != "" {
					c.closeErr = fmt.Errorf("docker socket proxy (run_as=%s): %w: %s", c.runAs, err, msg)
				} else {
					c.closeErr = fmt.Errorf("docker socket proxy (run_as=%s): %w", c.runAs, err)
				}
			}
		case <-time.After(5 * time.Second):
			_ = c.session.Signal(ssh.SIGTERM)
			c.closeErr = fmt.Errorf("docker socket proxy (run_as=%s): close timeout", c.runAs)
		}
		_ = c.session.Close()
	})
	return c.closeErr
}

func (c *sshSessionConn) LocalAddr() net.Addr                { return &dummyAddr{} }
func (c *sshSessionConn) RemoteAddr() net.Addr               { return &dummyAddr{} }
func (c *sshSessionConn) SetDeadline(_ time.Time) error      { return nil }
func (c *sshSessionConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *sshSessionConn) SetWriteDeadline(_ time.Time) error { return nil }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "ssh+dial-stdio" }
func (dummyAddr) String() string  { return "ssh+dial-stdio" }

// RunAsProxyCommandForTest returns the remote command used for run_as dial (tests).
func RunAsProxyCommandForTest(runAs, socketPath string) string {
	return runAsProxyCommand(runAs, socketPath)
}
