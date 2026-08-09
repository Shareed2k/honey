package webserver

import (
	"net"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestStartK8sProxyDisabledIsUnchanged proves that with Options.K8sProxy left
// nil (its zero value, and the default for every caller before this task),
// Start never launches a k8s-proxy listener: no "k8s-proxy" log line is emitted
// and the ordinary TCP listener serves and shuts down exactly as before.
func TestStartK8sProxyDisabledIsUnchanged(t *testing.T) {
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
		// K8sProxy intentionally omitted (nil): proxy disabled.
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := contextWithCancelAfter(t)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	waitForTCPServe(t, addr)

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	for _, entry := range logs.All() {
		if strings.Contains(strings.ToLower(entry.Message), "k8s-proxy") {
			t.Fatalf("unexpected k8s-proxy log line with K8sProxy=nil: %q", entry.Message)
		}
	}
}
