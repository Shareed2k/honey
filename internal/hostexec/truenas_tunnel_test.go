package hostexec

import (
	"context"
	"io"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestTruenasTunnelUsesAPIShell(t *testing.T) {
	applianceIP := hosts.Record{
		Provider: "truenas", PrimaryIP: "10.0.0.1",
		Meta: map[string]string{"kind": "appliance"},
	}
	if TruenasTunnelUsesAPIShell(applianceIP) {
		t.Fatal("appliance with IP should use SSH tunnel")
	}
	guest := hosts.Record{
		Provider: "truenas",
		Meta:     map[string]string{"kind": "virt_instance", "id": "abc"},
	}
	if !TruenasTunnelUsesAPIShell(guest) {
		t.Fatal("virt_instance without IP should use API shell tunnel")
	}
	vm := hosts.Record{
		Provider: "truenas", PrimaryIP: "10.0.0.5",
		Meta: map[string]string{"kind": "virt_instance", "id": "abc"},
	}
	if TruenasTunnelUsesAPIShell(vm) {
		t.Fatal("virt_instance with IP should use SSH tunnel")
	}
}

func TestForRecord_TruenasGuestUsesAPIShellExecutor(t *testing.T) {
	SetTrueNASRunTunnel(func(context.Context, string, hosts.Record, string, io.Writer) error {
		return nil
	})
	r := hosts.Record{
		Provider: "truenas",
		Meta:     map[string]string{"kind": "virt_instance", "id": "x"},
	}
	ex := ForRecord(r)
	if _, ok := ex.(truenasExecutor); !ok {
		t.Fatalf("expected truenasExecutor, got %T", ex)
	}
}
