package ui

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestRecipeTunnelCoordinator_registerLookup(t *testing.T) {
	t.Parallel()
	pool := NewGlobalTunnelPool(0)
	defer pool.Close()
	coord := NewRecipeTunnelCoordinator(pool)
	defer coord.Close()

	rec := hosts.Record{Name: "db1", PrimaryIP: "10.0.0.1", Provider: "gcp"}
	ep := TunnelEndpoint{Host: "127.0.0.1", Port: 15432, Mode: "local", RemoteHost: "localhost", RemotePort: 5432}
	coord.Register("pg_tunnel", "ubuntu", rec, ep, nil)

	got, ok := coord.Lookup("pg_tunnel", "ubuntu", rec)
	if !ok || got.Port != 15432 {
		t.Fatalf("lookup ok=%v ep=%+v", ok, got)
	}
}

func TestRecipeTunnelCoordinator_lookupEndpoint(t *testing.T) {
	t.Parallel()
	pool := NewGlobalTunnelPool(0)
	defer pool.Close()
	coord := NewRecipeTunnelCoordinator(pool)
	defer coord.Close()

	rec := hosts.Record{Name: "db1", PrimaryIP: "10.0.0.1", Provider: "gcp"}
	coord.Register("pg_tunnel", "ubuntu", rec, TunnelEndpoint{
		Host: "127.0.0.1", Port: 15432, Mode: "local",
	}, nil)

	host, port, ok := coord.LookupEndpoint("pg_tunnel", "ubuntu", rec)
	if !ok || host != "127.0.0.1" || port != 15432 {
		t.Fatalf("host=%q port=%d ok=%v", host, port, ok)
	}

	coord.Register("tun", "ubuntu", rec, TunnelEndpoint{Mode: "tun", TunName: "tun0"}, nil)
	_, _, ok = coord.LookupEndpoint("tun", "ubuntu", rec)
	if ok {
		t.Fatal("tun mode should not expose TCP endpoint")
	}
}
