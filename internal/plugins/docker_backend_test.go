package plugins

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/client"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

// fakeDockerBackend records DialShim/ShimHostPath use and redirects every shim
// dial to a fixed test-server address, so a test can prove the transport's
// shim HTTP client is wired through the backend seam (not a hardcoded dialer)
// — without any real Docker daemon.
type fakeDockerBackend struct {
	shimPath string
	dialAddr string // real address DialShim connects to (the fake plugin-init server)

	mu        sync.Mutex
	dialCalls int
}

func (b *fakeDockerBackend) Client() *client.Client { return nil }

func (b *fakeDockerBackend) ShimHostPath(context.Context) (string, error) { return b.shimPath, nil }

func (b *fakeDockerBackend) DialShim(ctx context.Context, network, _ string) (net.Conn, error) {
	b.mu.Lock()
	b.dialCalls++
	b.mu.Unlock()
	var d net.Dialer
	return d.DialContext(ctx, network, b.dialAddr)
}

func (b *fakeDockerBackend) Close() error { return nil }

// TestDockerTransport_ShimCallsRouteThroughBackendDialShim proves the shim
// HTTP client newDockerTransport builds dials through backend.DialShim — the
// seam that lets a remote backend tunnel the shim call over SSH. Same shim
// /call protocol regardless of backend.
func TestDockerTransport_ShimCallsRouteThroughBackendDialShim(t *testing.T) {
	srv := newFakePluginInitServer(t, func(apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Output: `{"ok":true}`, ExitCode: 0}
	})
	fake := &fakeDockerBackend{
		shimPath: "/sentinel/honey-plugin-init",
		dialAddr: strings.TrimPrefix(srv.URL, "http://"),
	}
	dt, err := newDockerTransport(context.Background(), fake, dockerTransportConfig{CueSource: []byte(testDockerCueSource)})
	if err != nil {
		t.Fatalf("newDockerTransport: %v", err)
	}
	// Bypass createContainer (it needs a real daemon): mark the transport
	// started with a bogus address — backend.DialShim ignores the address and
	// redirects to the fake server, so the /call still lands there.
	dt.mu.Lock()
	dt.addr = "http://plugin-init.invalid:49094"
	dt.mu.Unlock()
	dt.startMu.Lock()
	dt.started = true
	dt.startMu.Unlock()

	exit, out, err := dt.CallRaw(context.Background(), "scan", []byte(`{"target":"alpine"}`))
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit = %d, want 0", exit)
	}
	if !strings.Contains(string(out), `"ok":true`) {
		t.Fatalf("unexpected output: %s", out)
	}
	fake.mu.Lock()
	calls := fake.dialCalls
	fake.mu.Unlock()
	if calls == 0 {
		t.Fatal("backend.DialShim was never used — shim HTTP client not wired to the backend seam")
	}
}

// TestLocalBackend_Seams pins the default backend's behavior: ShimHostPath
// returns the configured operator path, and DialShim performs a real local
// dial (verified against a loopback listener).
func TestLocalBackend_Seams(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		if c, aerr := ln.Accept(); aerr == nil {
			_ = c.Close()
		}
	}()

	b := &localBackend{shimPath: "/opt/honey/honey-plugin-init"}
	got, err := b.ShimHostPath(context.Background())
	if err != nil {
		t.Fatalf("ShimHostPath: %v", err)
	}
	if got != "/opt/honey/honey-plugin-init" {
		t.Errorf("ShimHostPath = %q, want the configured path", got)
	}
	conn, err := b.DialShim(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("DialShim: %v", err)
	}
	_ = conn.Close()
}
