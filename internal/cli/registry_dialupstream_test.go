package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

// fakeCloser is a fake io.Closer that records whether Close was called.
type fakeCloser struct {
	closed bool
	err    error
}

func (f *fakeCloser) Close() error {
	f.closed = true
	return f.err
}

// fakeConn is a minimal net.Conn fake that records whether Close was called.
// Only Close is exercised by these tests; the rest satisfy the interface.
type fakeConn struct {
	net.Conn
	closed bool
	err    error
}

func (f *fakeConn) Close() error {
	f.closed = true
	return f.err
}

// TestSSHDialConn_Close_ClosesUnderlyingClient asserts that sshDialConn.Close()
// closes both the wrapped net.Conn and the borrowed SSH client (closer), so
// DialUpstream never leaks an SSH session when a caller closes the returned conn.
func TestSSHDialConn_Close_ClosesUnderlyingClient(t *testing.T) {
	fc := &fakeConn{}
	closer := &fakeCloser{}

	dc := &sshDialConn{Conn: fc, closer: closer}

	if err := dc.Close(); err != nil {
		t.Fatalf("Close() returned unexpected error: %v", err)
	}
	if !fc.closed {
		t.Error("expected wrapped net.Conn to be closed")
	}
	if !closer.closed {
		t.Error("expected SSH client closer to be closed")
	}
}

// TestSSHDialConn_Close_PropagatesConnError asserts the conn's Close error is
// returned even though the closer is also closed.
func TestSSHDialConn_Close_PropagatesConnError(t *testing.T) {
	wantErr := errors.New("boom")
	fc := &fakeConn{err: wantErr}
	closer := &fakeCloser{}

	dc := &sshDialConn{Conn: fc, closer: closer}

	if err := dc.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("Close() = %v, want %v", err, wantErr)
	}
	if !closer.closed {
		t.Error("expected SSH client closer to be closed even when conn.Close() errors")
	}
}

// TestSSHDialConn_Close_NilCloser asserts Close() tolerates a nil closer
// (defensive: sshDialConn should never panic if closer is unset).
func TestSSHDialConn_Close_NilCloser(t *testing.T) {
	fc := &fakeConn{}
	dc := &sshDialConn{Conn: fc}

	if err := dc.Close(); err != nil {
		t.Fatalf("Close() returned unexpected error: %v", err)
	}
	if !fc.closed {
		t.Error("expected wrapped net.Conn to be closed")
	}
}

// TestSSHFallbackExecutor_DialUpstream_DialError asserts DialUpstream surfaces a
// real, wrapped error (not the old "not implemented" stub) when the underlying
// SSH dial cannot even start (e.g. no host IP on the record).
func TestSSHFallbackExecutor_DialUpstream_DialError(t *testing.T) {
	e := &sshFallbackExecutor{}
	rec := hosts.Record{} // no PrimaryIP -> Dial fails fast, no network I/O

	conn, err := e.DialUpstream(context.Background(), "", rec, "127.0.0.1:1")
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("expected error for record with no host ip")
	}
	if conn != nil {
		t.Error("expected nil conn on error")
	}
	if strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("DialUpstream still returns the not-implemented stub: %v", err)
	}
	if !strings.Contains(err.Error(), "ssh dial for upstream") {
		t.Fatalf("expected error to be wrapped with %q context, got: %v", "ssh dial for upstream", err)
	}
}

// compile-time interface check: sshDialConn must satisfy net.Conn.
var _ net.Conn = (*sshDialConn)(nil)

// compile-time check the fake conn also satisfies net.Conn for the tests above.
var (
	_ net.Conn  = (*fakeConn)(nil)
	_ io.Closer = (*fakeCloser)(nil)
)
