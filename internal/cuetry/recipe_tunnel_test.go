package cuetry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestEffectiveTunnelMode_defaultsLocal(t *testing.T) {
	t.Parallel()
	if got := EffectiveTunnelMode(nil); got != "local" {
		t.Fatalf("got %q", got)
	}
	if got := EffectiveTunnelMode(&RecipeStepTunnel{}); got != "local" {
		t.Fatalf("got %q", got)
	}
}

func TestEffectiveTunnelMode_protocolUDP(t *testing.T) {
	t.Parallel()
	got := EffectiveTunnelMode(&RecipeStepTunnel{Protocol: "udp"})
	if got != "udp" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateStepTunnel_localRequiresRemotePort(t *testing.T) {
	t.Parallel()
	err := validateStepTunnel(0, StepKindTunnel, RecipeStep{
		Host:   "db-*",
		Tunnel: &RecipeStepTunnel{Mode: "local"},
	}, ExecutionModeLinear)
	if err == nil || !strings.Contains(err.Error(), "remote_port") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateStepTunnel_udpRequiresSocat(t *testing.T) {
	t.Parallel()
	err := validateStepTunnel(0, StepKindTunnel, RecipeStep{
		Host: "db-*",
		Tunnel: &RecipeStepTunnel{
			Mode:       "udp",
			RemotePort: 53,
		},
	}, ExecutionModeLinear)
	if err == nil || !strings.Contains(err.Error(), "remote_socat") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateRecipeTunnelRefs_unknownStep(t *testing.T) {
	t.Parallel()
	cfg, _ := json.Marshal(map[string]string{"tunnel_step": "missing"})
	steps := []RecipeStep{{
		Host: "*",
		Plugin: &RecipeStepPlugin{
			ID:     "postgres",
			Action: "query",
			Config: cfg,
		},
	}}
	err := validateRecipeTunnelRefs(steps)
	if err == nil || !strings.Contains(err.Error(), "unknown step id") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateRecipeTunnelRefs_requiresTunnelKind(t *testing.T) {
	t.Parallel()
	cfg, _ := json.Marshal(map[string]string{"tunnel_step": "fetch"})
	steps := []RecipeStep{
		{ID: "fetch", Host: "*", Command: "echo"},
		{ID: "pg", Host: "*", Plugin: &RecipeStepPlugin{ID: "postgres", Action: "query", Config: cfg}},
	}
	err := validateRecipeTunnelRefs(steps)
	if err == nil || !strings.Contains(err.Error(), "not a tunnel step") {
		t.Fatalf("got %v", err)
	}
}

func TestParseRemoteRecipe_tunnelWithPostgresRef(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "pg-tunnel"
	type: "graph"
	steps: [
		{
			id: "pg_tunnel"
			host: "db-*"
			tunnel: {
				remote_host: "localhost"
				remote_port: 5432
				share_key: "db-primary-pg"
			}
		},
		{
			id: "query"
			host: "db-*"
			depends: ["pg_tunnel"]
			plugin: {
				id: "postgres"
				action: "query"
				config: {
					dsn_secret: "PG_DSN"
					tunnel_step: "pg_tunnel"
					sql: "SELECT 1"
					params: []
				}
			}
		},
	]
}
`
	rec := []hosts.Record{{Name: "db1", PrimaryIP: "10.0.0.1", Provider: "gcp"}}
	r, err := ParseRemoteRecipe([]byte(cue), rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 2 {
		t.Fatalf("steps=%d", len(r.Steps))
	}
	k, err := ClassifyStep(r.Steps[0])
	if err != nil || k != StepKindTunnel {
		t.Fatalf("kind=%v err=%v", k, err)
	}
}
