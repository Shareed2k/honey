package mobile

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

type fakeVPNCallback struct {
	states []string
	stats  []string
}

func (f *fakeVPNCallback) OnState(s string) { f.states = append(f.states, s) }
func (f *fakeVPNCallback) OnStats(s string) { f.stats = append(f.stats, s) }

func TestStartVPN_rejectsWhenRunning(t *testing.T) {
	vpnMu.Lock()
	vpnCurrent = &vpnSession{cancel: func() {}}
	vpnMu.Unlock()
	t.Cleanup(func() {
		vpnMu.Lock()
		vpnCurrent = nil
		vpnMu.Unlock()
	})

	cb := &fakeVPNCallback{}
	err := StartVPN(7, `{}`, cb)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected 'already running' error, got %v", err)
	}
}

func TestStopVPN_noopWhenIdle(t *testing.T) {
	vpnMu.Lock()
	vpnCurrent = nil
	vpnMu.Unlock()
	if err := StopVPN(); err != nil {
		t.Fatalf("StopVPN on idle: %v", err)
	}
}

func TestResolveExitNode_errorOnUnknownBackend(t *testing.T) {
	_, err := ResolveExitNode(`{"backends":"dummy-nonexistent-backend","name":"x"}`)
	if err == nil {
		t.Fatal("expected error for unknown backend, got nil")
	}
}

func TestResolveExitNode_badJSON(t *testing.T) {
	if _, err := ResolveExitNode(`{not json`); err == nil {
		t.Fatal("expected JSON parse error, got nil")
	}
}

func TestStartVPN_badJSON(t *testing.T) {
	if err := StartVPN(3, `{not json`, nil); err == nil {
		t.Fatal("expected JSON parse error, got nil")
	}
}

// TestResolveExit_DirectIP verifies that when host_ip is supplied, resolveExit
// returns that IP directly without touching the inventory search path.
func TestResolveExit_DirectIP(t *testing.T) {
	tests := []struct {
		name     string
		req      vpnRequest
		wantIP   string
		wantPort int
		wantName string
	}{
		{
			name:     "ip with custom port",
			req:      vpnRequest{Name: "prod-eu-1", HostIP: "1.2.3.4", SSHPort: 2222},
			wantIP:   "1.2.3.4",
			wantPort: 2222,
			wantName: "prod-eu-1",
		},
		{
			name:     "ip without port defaults to zero",
			req:      vpnRequest{Name: "h", HostIP: "10.0.0.5"},
			wantIP:   "10.0.0.5",
			wantPort: 0,
			wantName: "h",
		},
		{
			name:     "ip is trimmed",
			req:      vpnRequest{Name: "h", HostIP: "  10.0.0.6  "},
			wantIP:   "10.0.0.6",
			wantPort: 0,
			wantName: "h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, ip, port, err := resolveExit(tt.req)
			if err != nil {
				t.Fatalf("resolveExit returned error: %v", err)
			}
			if ip != tt.wantIP {
				t.Errorf("ip = %q, want %q", ip, tt.wantIP)
			}
			if port != tt.wantPort {
				t.Errorf("port = %d, want %d", port, tt.wantPort)
			}
			if rec.Name != tt.wantName {
				t.Errorf("rec.Name = %q, want %q", rec.Name, tt.wantName)
			}
			if rec.PrimaryIP != tt.wantIP {
				t.Errorf("rec.PrimaryIP = %q, want %q", rec.PrimaryIP, tt.wantIP)
			}
		})
	}
}

func TestStartVPN_InvalidFD(t *testing.T) {
	cb := &fakeVPNCallback{}
	err := StartVPN(-1, `{}`, cb)
	if err == nil {
		t.Fatal("expected error for invalid fd, got nil")
	}
	if !strings.Contains(err.Error(), "invalid tun fd") {
		t.Errorf("expected 'invalid tun fd', got %v", err)
	}
	if len(cb.states) == 0 || cb.states[0] != "error" {
		t.Errorf("expected state 'error', got %v", cb.states)
	}
}

func TestEmitState(t *testing.T) {
	cb := &fakeVPNCallback{}
	emitState(cb, "test-state")
	if len(cb.states) == 0 || cb.states[0] != "test-state" {
		t.Errorf("expected test-state, got %v", cb.states)
	}

	// Ensure nil callback doesn't panic
	emitState(nil, "test")
}

func TestPumpStats(_ *testing.T) {
	cb := &fakeVPNCallback{}
	ctx, cancel := context.WithCancel(context.Background())
	// cancel immediately so it exits right after first tick or without tick
	cancel()

	pumpStats(ctx, cb)
	// should not block

	// Test nil cb
	pumpStats(ctx, nil)
}

// TestResolveExit_DirectIPSkipsSearch proves the IP path bypasses inventory: an
// unknown backend errors via the search path (see
// TestResolveExitNode_errorOnUnknownBackend) but succeeds once host_ip is set.
func TestResolveExit_DirectIPSkipsSearch(t *testing.T) {
	_, ip, _, err := resolveExit(vpnRequest{Backends: "dummy-nonexistent-backend", Name: "x", HostIP: "9.9.9.9"})
	if err != nil {
		t.Fatalf("expected no error with host_ip set, got %v", err)
	}
	if ip != "9.9.9.9" {
		t.Errorf("ip = %q, want 9.9.9.9", ip)
	}
}

// fakeExecutor is a local hand-rolled stub matching internal/hostexec.Executor
// exactly. Only DialUpstream is exercised by the honeyprovider VPN path.
type fakeExecutor struct {
	dialCalls []fakeDialCall
	conn      net.Conn
	err       error
}

type fakeDialCall struct {
	user    string
	rec     hosts.Record
	address string
}

func (f *fakeExecutor) Dial(_ string, _ hosts.Record) (hostexec.HostClient, error) {
	return nil, nil
}
func (f *fakeExecutor) RunInteractive(_ string, _ hosts.Record) error { return nil }
func (f *fakeExecutor) RunTunnel(_ context.Context, _ string, _ hosts.Record, _ string, _ io.Writer) error {
	return nil
}

func (f *fakeExecutor) DialUpstream(_ context.Context, user string, rec hosts.Record, address string) (net.Conn, error) {
	f.dialCalls = append(f.dialCalls, fakeDialCall{user: user, rec: rec, address: address})
	return f.conn, f.err
}

type fakeExecRegistry struct {
	ex *fakeExecutor
}

func (f *fakeExecRegistry) ForRecord(_ hosts.Record) hostexec.Executor { return f.ex }
func (f *fakeExecRegistry) Reconfigure(_ *config.File)                 {}
func (f *fakeExecRegistry) RunSSHTunnel(_ context.Context, _, _ string, _ int, _ string, _ io.Writer) error {
	return nil
}
func (f *fakeExecRegistry) BorrowSSH(_ string, _ hosts.Record) (any, bool) { return nil, false }

// TestDialerForExit_HoneyproviderPath proves a record carrying
// Meta["honey_upstream_backend"] takes the honeyExecDialer branch — no SSH
// pool is built, and garbage SSHIdentityFile/SSHIdentityPassphrase values are
// never touched (dialerForExit would error out trying to parse them as a real
// key if the raw-SSH branch were mistakenly taken instead).
func TestDialerForExit_HoneyproviderPath(t *testing.T) {
	rec := hosts.Record{
		Name:      "db-1",
		PrimaryIP: "10.1.2.3",
		Meta:      map[string]string{"honey_upstream_backend": "prod-honey"},
	}
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close(); serverConn.Close() })
	fx := &fakeExecutor{conn: clientConn}

	orig := execRegistry
	execRegistry = func() hostexec.Registry { return &fakeExecRegistry{ex: fx} }
	t.Cleanup(func() { execRegistry = orig })

	req := vpnRequest{
		SSHIdentityFile:       "not-a-real-pem-should-never-be-read",
		SSHIdentityPassphrase: "irrelevant",
	}
	dialer, pool, err := dialerForExit(context.Background(), req, rec, "ubuntu", "10.1.2.3", 22)
	if err != nil {
		t.Fatalf("dialerForExit: %v", err)
	}
	if pool != nil {
		t.Fatalf("expected nil pool for honeyprovider path, got %v", pool)
	}
	hd, ok := dialer.(*honeyExecDialer)
	if !ok {
		t.Fatalf("expected *honeyExecDialer, got %T", dialer)
	}
	if hd.ex != fx || hd.user != "ubuntu" || hd.rec.Name != "db-1" {
		t.Errorf("honeyExecDialer not wired correctly: %+v", hd)
	}
	// The pre-flight probe (one DialUpstream call to ip:sshPort) should have
	// happened during dialerForExit itself.
	if len(fx.dialCalls) != 1 {
		t.Fatalf("expected 1 probe DialUpstream call, got %d", len(fx.dialCalls))
	}
	if fx.dialCalls[0].address != "10.1.2.3:22" {
		t.Errorf("probe address = %q, want 10.1.2.3:22", fx.dialCalls[0].address)
	}
}

// TestDialerForExit_HoneyproviderProbeFailure proves a failing pre-flight
// probe surfaces as a dialerForExit error (so StartVPN fails fast) instead of
// only showing up later as a per-connection SOCKS5 dial failure.
func TestDialerForExit_HoneyproviderProbeFailure(t *testing.T) {
	rec := hosts.Record{
		Name:      "db-1",
		PrimaryIP: "10.1.2.3",
		Meta:      map[string]string{"honey_upstream_backend": "prod-honey"},
	}
	fx := &fakeExecutor{err: fmt.Errorf("mesh not warmed up")}

	orig := execRegistry
	execRegistry = func() hostexec.Registry { return &fakeExecRegistry{ex: fx} }
	t.Cleanup(func() { execRegistry = orig })

	_, _, err := dialerForExit(context.Background(), vpnRequest{}, rec, "ubuntu", "10.1.2.3", 22)
	if err == nil {
		t.Fatal("expected probe failure to propagate, got nil")
	}
}

// TestHoneyExecDialer_ForwardsToDialUpstream proves both sshclient.SSHDialer
// (Dial) and sshclient's contextDialer fast path (DialContext) forward the
// exact user/record/address to Executor.DialUpstream, with no real
// network/websocket involved.
func TestHoneyExecDialer_ForwardsToDialUpstream(t *testing.T) {
	rec := hosts.Record{Name: "db-1", PrimaryIP: "10.1.2.3"}
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close(); serverConn.Close() })

	fx := &fakeExecutor{conn: clientConn}
	d := &honeyExecDialer{ex: fx, user: "ubuntu", rec: rec}

	if _, err := d.DialContext(context.Background(), "tcp", "127.0.0.1:5432"); err != nil {
		t.Fatalf("DialContext: %v", err)
	}
	if _, err := d.Dial("tcp", "127.0.0.1:5432"); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if len(fx.dialCalls) != 2 {
		t.Fatalf("expected 2 DialUpstream calls, got %d", len(fx.dialCalls))
	}
	for _, call := range fx.dialCalls {
		if call.user != "ubuntu" || call.address != "127.0.0.1:5432" || call.rec.Name != "db-1" {
			t.Errorf("unexpected DialUpstream args: %+v", call)
		}
	}
}

// TestDialerForExit_RawSSHPath_NoHoneyMeta proves a record with no
// honey_upstream_backend meta still takes the raw-SSH branch (a real, if
// doomed-to-fail-fast, SSH pool dial attempt) — guards against the Meta check
// accidentally being too broad.
func TestDialerForExit_RawSSHPath_NoHoneyMeta(t *testing.T) {
	rec := hosts.Record{Name: "plain-host", PrimaryIP: "127.0.0.1"}
	req := vpnRequest{PoolSize: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := dialerForExit(ctx, req, rec, "ubuntu", "127.0.0.1", 1); err == nil {
		t.Fatal("expected raw SSH pool dial to fail against a closed port, got nil error")
	}
}
