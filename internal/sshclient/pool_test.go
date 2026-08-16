package sshclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/melbahja/goph"
	gossh "golang.org/x/crypto/ssh"
)

// newTestHoneyClient wraps a loopback *ssh.Client in a minimal HoneyClient.
// The server is cleaned up when t ends via t.Cleanup.
func newTestHoneyClient(t *testing.T) *HoneyClient {
	t.Helper()
	sshClient, cleanup := newLoopbackSSHClient(t)
	t.Cleanup(func() {
		_ = sshClient.Close()
		cleanup()
	})
	return &HoneyClient{
		Client: &goph.Client{
			Client: sshClient,
			Config: &goph.Config{},
		},
	}
}

func TestNewSSHPool_size1(t *testing.T) {
	ctx := context.Background()
	calls := 0
	dialFn := func() (*HoneyClient, error) {
		calls++
		hc := newTestHoneyClient(t)
		return hc, nil
	}
	pool, err := NewSSHPool(ctx, 1, dialFn)
	if err != nil {
		t.Fatalf("NewSSHPool: %v", err)
	}
	defer pool.Close()
	if calls != 1 {
		t.Errorf("expected 1 dial, got %d", calls)
	}
	if s := pool.pool.Stat(); s.TotalResources() != 1 {
		t.Errorf("expected 1 pool resource, got %d", s.TotalResources())
	}
}

func TestNewSSHPool_size3(t *testing.T) {
	ctx := context.Background()
	calls := 0
	dialFn := func() (*HoneyClient, error) {
		calls++
		hc := newTestHoneyClient(t)
		return hc, nil
	}
	pool, err := NewSSHPool(ctx, 3, dialFn)
	if err != nil {
		t.Fatalf("NewSSHPool: %v", err)
	}
	defer pool.Close()
	if calls != 3 {
		t.Errorf("expected 3 dials, got %d", calls)
	}
}

func TestSSHPool_Dial(t *testing.T) {
	ctx := context.Background()
	dialFn := func() (*HoneyClient, error) {
		hc := newTestHoneyClient(t)
		return hc, nil
	}
	pool, err := NewSSHPool(ctx, 2, dialFn)
	if err != nil {
		t.Fatalf("NewSSHPool: %v", err)
	}
	defer pool.Close()

	// Dial a TCP connection through the pool (loopback server accepts direct-tcpip).
	conn, err := pool.Dial("tcp", "127.0.0.1:9")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	_ = conn.Close()
}

func TestSSHPool_RunWithClient(t *testing.T) {
	ctx := context.Background()
	dialFn := func() (*HoneyClient, error) {
		hc := newTestHoneyClient(t)
		return hc, nil
	}
	pool, err := NewSSHPool(ctx, 1, dialFn)
	if err != nil {
		t.Fatalf("NewSSHPool: %v", err)
	}
	defer pool.Close()

	var seen *gossh.Client
	if err := pool.RunWithClient(ctx, func(c *gossh.Client) error {
		seen = c
		return nil
	}); err != nil {
		t.Fatalf("RunWithClient: %v", err)
	}
	if seen == nil {
		t.Error("fn was called with nil client")
	}
}

func TestSSHPool_Close(t *testing.T) {
	ctx := context.Background()
	dialFn := func() (*HoneyClient, error) {
		hc := newTestHoneyClient(t)
		return hc, nil
	}
	pool, err := NewSSHPool(ctx, 1, dialFn)
	if err != nil {
		t.Fatalf("NewSSHPool: %v", err)
	}
	pool.Close()
	// Dial after close should return an error.
	_, err = pool.Dial("tcp", "127.0.0.1:9")
	if err == nil {
		t.Error("expected error dialing closed pool")
	}
}

// Compile-time check: *SSHPool and *gossh.Client both satisfy SSHDialer.
var (
	_ SSHDialer = (*SSHPool)(nil)
	_ SSHDialer = (*gossh.Client)(nil)
)

func newRejectingLoopbackSSHClient(t *testing.T) (*gossh.Client, func()) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	serverCfg := &gossh.ServerConfig{NoClientAuth: true}
	serverCfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for {
			raw, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go func(raw net.Conn) {
				_, chans, reqs, serr := gossh.NewServerConn(raw, serverCfg)
				if serr != nil {
					_ = raw.Close()
					return
				}
				go gossh.DiscardRequests(reqs)
				for newCh := range chans {
					switch newCh.ChannelType() {
					case "direct-tcpip":
						var msg struct {
							Raddr string
							Rport uint32
							Laddr string
							Lport uint32
						}
						_ = gossh.Unmarshal(newCh.ExtraData(), &msg)
						if msg.Rport == 9999 {
							_ = newCh.Reject(gossh.ConnectionFailed, "connect failed (Connection refused)")
							continue
						}
						ch, inReqs, chErr := newCh.Accept()
						if chErr != nil {
							continue
						}
						go gossh.DiscardRequests(inReqs)
						_, _ = io.Copy(io.Discard, ch)
						_ = ch.Close()
					default:
						_ = newCh.Reject(gossh.UnknownChannelType, "unsupported")
					}
				}
			}(raw)
		}
	}()

	client, err := gossh.Dial("tcp", ln.Addr().String(), &gossh.ClientConfig{
		User:            "test",
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, func() {
		_ = client.Close()
		_ = ln.Close()
		<-stop
	}
}

func TestSSHPool_Dial_ChannelOpenFailureDoesNotDestroyPool(t *testing.T) {
	ctx := context.Background()
	var dialCalls atomic.Int32
	dialFn := func() (*HoneyClient, error) {
		dialCalls.Add(1)
		sshClient, cleanup := newRejectingLoopbackSSHClient(t)
		t.Cleanup(func() {
			_ = sshClient.Close()
			cleanup()
		})
		return &HoneyClient{
			Client: &goph.Client{
				Client: sshClient,
				Config: &goph.Config{},
			},
		}, nil
	}

	pool, err := NewSSHPool(ctx, 1, dialFn)
	if err != nil {
		t.Fatalf("NewSSHPool: %v", err)
	}
	defer pool.Close()

	if dialCalls.Load() != 1 {
		t.Fatalf("expected 1 initial dial, got %d", dialCalls.Load())
	}

	start := time.Now()
	// Port 9999 triggers connection refused (OpenChannelError with ConnectionFailed).
	conn, err := pool.Dial("tcp", "127.0.0.1:9999")
	if conn != nil {
		_ = conn.Close()
		t.Fatal("expected dial error on refused port, got nil")
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("dial took %v, expected fast failure without backoff retry loop", elapsed)
	}

	openErr, ok := errors.AsType[*gossh.OpenChannelError](err)
	if !ok {
		t.Fatalf("expected OpenChannelError, got %T: %v", err, err)
	}
	if openErr.Reason != gossh.ConnectionFailed {
		t.Fatalf("expected ConnectionFailed reason, got %v", openErr.Reason)
	}

	// Crucial: the SSH connection must NOT have been destroyed or redialed!
	if calls := dialCalls.Load(); calls != 1 {
		t.Errorf("expected exactly 1 SSH dial (no pool recreation), got %d dials", calls)
	}
	if s := pool.pool.Stat(); s.TotalResources() != 1 || s.IdleResources() != 1 {
		t.Errorf("expected 1 total/idle pool resource, got total=%d idle=%d", s.TotalResources(), s.IdleResources())
	}

	// Subsequent dial to a working port must succeed using the existing connection.
	conn2, err := pool.Dial("tcp", "127.0.0.1:9")
	if err != nil {
		t.Fatalf("subsequent Dial failed: %v", err)
	}
	_ = conn2.Close()

	if calls := dialCalls.Load(); calls != 1 {
		t.Errorf("expected connection reuse without additional dials, got %d dials", calls)
	}
}

func TestSSHPool_Dial_DeadConnectionDestroysEntryAndReconstructs(t *testing.T) {
	ctx := context.Background()
	var dialCalls atomic.Int32
	var lastSSH *gossh.Client
	dialFn := func() (*HoneyClient, error) {
		dialCalls.Add(1)
		sshClient, cleanup := newLoopbackSSHClient(t)
		lastSSH = sshClient
		t.Cleanup(func() {
			_ = sshClient.Close()
			cleanup()
		})
		return &HoneyClient{
			Client: &goph.Client{
				Client: sshClient,
				Config: &goph.Config{},
			},
		}, nil
	}

	pool, err := NewSSHPool(ctx, 1, dialFn)
	if err != nil {
		t.Fatalf("NewSSHPool: %v", err)
	}
	defer pool.Close()

	if dialCalls.Load() != 1 {
		t.Fatalf("expected 1 initial dial, got %d", dialCalls.Load())
	}

	// First dial succeeds.
	conn, err := pool.Dial("tcp", "127.0.0.1:9")
	if err != nil {
		t.Fatalf("first Dial failed: %v", err)
	}
	_ = conn.Close()

	// Intentionally kill the underlying SSH client connection to simulate network drop.
	if lastSSH != nil {
		_ = lastSSH.Close()
	}

	// Dialing through the pool should detect transport error, destroy the dead entry,
	// and retry by dialing a fresh SSH client via dialFn.
	conn2, err := pool.Dial("tcp", "127.0.0.1:9")
	if err != nil {
		t.Fatalf("Dial after connection death failed: %v", err)
	}
	_ = conn2.Close()

	if calls := dialCalls.Load(); calls != 2 {
		t.Errorf("expected 2 total dials (reconstructed after death), got %d dials", calls)
	}
}
