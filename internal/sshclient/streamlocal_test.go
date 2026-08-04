package sshclient

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartLocalSocketForward_roundTrip(t *testing.T) {
	client, cleanup := newLoopbackSSHClient(t)
	defer cleanup()

	// A short base dir: unix sun_path caps at ~104 bytes, and t.TempDir() on
	// macOS (/var/folders/…) already blows past that with the socket suffix.
	dir, err := os.MkdirTemp("/tmp", "hpg")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	local := filepath.Join(dir, ".s.PGSQL.5432")
	remote := "/var/run/postgresql/.s.PGSQL.5432"

	path, stop, err := StartLocalSocketForward(context.Background(), client, local, remote)
	if err != nil {
		t.Fatalf("StartLocalSocketForward: %v", err)
	}
	defer stop()
	if path != local {
		t.Fatalf("path = %q, want %q", path, local)
	}

	c, err := net.DialTimeout("unix", local, 2*time.Second)
	if err != nil {
		t.Fatalf("dial local socket: %v", err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Write([]byte("ping\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got := string(buf[:n]); got != "echo:ping\n" {
		t.Fatalf("round-trip = %q, want %q", got, "echo:ping\n")
	}

	stop()
	if _, statErr := os.Stat(local); !os.IsNotExist(statErr) {
		t.Fatalf("socket file not removed after stop: err=%v", statErr)
	}
}

func TestStartLocalSocketForward_relativePathRejected(t *testing.T) {
	client, cleanup := newLoopbackSSHClient(t)
	defer cleanup()

	if _, _, err := StartLocalSocketForward(context.Background(), client, "rel.sock", "/x"); err == nil {
		t.Fatal("expected error for relative local socket path")
	}
	if _, _, err := StartLocalSocketForward(context.Background(), client, "/tmp/ok.sock", "rel"); err == nil {
		t.Fatal("expected error for relative remote socket path")
	}
}

func TestDialStreamLocal_nilClient(t *testing.T) {
	if _, err := DialStreamLocal(nil, "/x"); err == nil {
		t.Fatal("expected error for nil client")
	}
}
