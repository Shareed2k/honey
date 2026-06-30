//go:build ignore

package mobile

import (
	"strings"
	"testing"
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
