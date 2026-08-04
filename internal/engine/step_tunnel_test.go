package engine

import (
	"encoding/json"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestTunnelEndpointJSON_unixEmitsSocketNotPort(t *testing.T) {
	t.Parallel()
	out := tunnelEndpointJSON(TunnelEndpoint{Mode: "unix", SocketPath: "/tmp/honey-pgsock-x/.s.PGSQL.5432"})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if m["mode"] != "unix" {
		t.Fatalf("mode = %v", m["mode"])
	}
	if m["socket"] != "/tmp/honey-pgsock-x/.s.PGSQL.5432" {
		t.Fatalf("socket = %v", m["socket"])
	}
	// A unix endpoint carries no meaningful tcp port; port defaults to 0 and
	// must not be advertised as a usable endpoint.
	if p, ok := m["port"]; ok && p != float64(0) {
		t.Fatalf("unix endpoint should not advertise a port, got %v", p)
	}
}

func TestTunnelEndpointJSON_tcpUnchanged(t *testing.T) {
	t.Parallel()
	out := tunnelEndpointJSON(TunnelEndpoint{Host: "127.0.0.1", Port: 15432, Mode: "local", RemoteHost: "localhost", RemotePort: 5432})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if m["host"] != "127.0.0.1" || m["port"] != float64(15432) || m["mode"] != "local" {
		t.Fatalf("tcp endpoint json changed: %v", m)
	}
	if _, ok := m["socket"]; ok {
		t.Fatalf("tcp endpoint must not carry a socket key: %v", m)
	}
}

func TestTunnelDryRunJSON_unix(t *testing.T) {
	t.Parallel()
	out := tunnelDryRunJSON(&cuetry.RecipeStepTunnel{Mode: "unix", RemoteSocket: "/var/run/postgresql/.s.PGSQL.5432"})
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if m["mode"] != "unix" || m["socket"] != "<<socket>>" {
		t.Fatalf("unix dry-run json: %v", m)
	}
	if _, ok := m["port"]; ok {
		t.Fatalf("unix dry-run must not carry port: %v", m)
	}
}
