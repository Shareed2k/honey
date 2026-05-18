package webserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/metrics"
)

func TestMetricsListenEndpoint(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	reg := metrics.NewRegistry("test", "test")
	srv, err := NewServer(Options{
		ListenAddr:        "127.0.0.1:0",
		Token:             "test-token",
		MetricsListenAddr: addr,
		Metrics:           reg,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := contextWithCancelAfter(t, 3*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(ctx) }()

	var body string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/metrics")
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			body = string(b)
			break
		}
	}
	if body == "" {
		t.Fatal("metrics endpoint did not become ready")
	}
	if !strings.Contains(body, "honey_build_info") {
		t.Errorf("metrics body missing honey_build_info")
	}
	cancel()
	<-errCh
}

func contextWithCancelAfter(t *testing.T, d time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(d)
		cancel()
	}()
	return ctx, cancel
}
