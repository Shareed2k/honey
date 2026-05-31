package sshclient

import (
	"context"
	"testing"

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
