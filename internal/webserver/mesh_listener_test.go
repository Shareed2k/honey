package webserver

import (
	"net"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// waitForTCPServe polls addr until something is accepting TCP connections (or
// the timeout elapses), proving Start's ordinary listener came up and is
// serving regardless of whatever happened on the (optional) mesh listener.
func waitForTCPServe(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", addr)
}

// TestStartMeshDisabledIsUnchanged proves that with Options.EnableMesh left at
// its zero value (false — the default for every existing caller of Start
// before this task), Start never even attempts to reach internal/meshnet: no
// mesh-related log line is ever emitted, and the ordinary TCP listener serves
// exactly as before.
//
// internal/meshnet is a real, singleton, package-level dependency: it doesn't
// currently expose a seam a webserver-package test could swap in a fake
// through (and this task is explicitly not allowed to modify internal/meshnet
// itself to add one). Rather than build a heavyweight substitute for that
// singleton, this test asserts the observable behavior the brief calls out
// directly: with the flag off, Start's log output contains no mention of
// "mesh" at all, which is only possible if the new `if s.opts.EnableMesh`
// branch was never entered — i.e., meshnet.Listener() was never called.
func TestStartMeshDisabledIsUnchanged(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	srv, err := NewServer(Options{
		ListenAddr: addr,
		Token:      "test-token",
		// EnableMesh intentionally omitted (zero value / false).
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := contextWithCancelAfter(t, 3*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForTCPServe(t, addr)

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	for _, entry := range logs.All() {
		if strings.Contains(strings.ToLower(entry.Message), "mesh") {
			t.Fatalf("unexpected mesh-related log line with EnableMesh=false: %q", entry.Message)
		}
	}
}

// TestStartMeshEnabledButNotStartedWarnsAndKeepsServing exercises the
// realistic case this task must handle without ever calling meshnet.Start
// itself (that's internal/cli/web.go's job, in a later task): EnableMesh is
// true, but the process-wide meshnet singleton was never started, so
// meshnet.Listener() returns its real "meshnet: not started" error. This test
// deliberately does not fake or stub internal/meshnet — it lets the real
// singleton (which starts every test binary in its unstarted zero state,
// since nothing in this package ever calls meshnet.Start) produce that error
// organically, giving genuine signal that Start's error-handling path (log a
// warning, keep going) is reachable and correct rather than merely mocked to
// look correct.
//
// It asserts both required-by-the-brief outcomes: (1) a warning is logged
// naming the mesh listener as unavailable, and (2) the ordinary TCP listener
// still comes up and serves — i.e. Start never returns an error and never
// aborts just because the mesh isn't up.
func TestStartMeshEnabledButNotStartedWarnsAndKeepsServing(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	restore := zap.ReplaceGlobals(zap.New(core))
	defer restore()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	srv, err := NewServer(Options{
		ListenAddr: addr,
		Token:      "test-token",
		EnableMesh: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := contextWithCancelAfter(t, 3*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	// The plain TCP listener must come up and serve regardless of the mesh
	// listener's fate.
	waitForTCPServe(t, addr)

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v (mesh unavailability must never fail Start)", err)
	}

	var sawWarning bool
	for _, entry := range logs.All() {
		if entry.Level == zapcore.WarnLevel && strings.Contains(entry.Message, "mesh listener unavailable") {
			sawWarning = true
			break
		}
	}
	if !sawWarning {
		t.Fatal("expected a warning log about the mesh listener being unavailable")
	}
}
