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
	err := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel:   &RecipeStepTunnel{Mode: "local"},
	}).Validate(StepValidateCtx{Index: 0, Mode: ExecutionModeLinear})
	if err == nil || !strings.Contains(err.Error(), "remote_port") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateStepTunnel_udpRequiresSocat(t *testing.T) {
	t.Parallel()
	err := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel: &RecipeStepTunnel{
			Mode:       "udp",
			RemotePort: 53,
		},
	}).Validate(StepValidateCtx{Index: 0, Mode: ExecutionModeLinear})
	if err == nil || !strings.Contains(err.Error(), "remote_socat") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateRecipeTunnelRefs_unknownStep(t *testing.T) {
	t.Parallel()
	cfg, _ := json.Marshal(map[string]string{"tunnel_step": "missing"})
	steps := wrapAll(&PluginStep{
		StepBase: StepBase{Host: "*"},
		Plugin: &RecipeStepPlugin{
			ID:     "postgres",
			Action: "query",
			Config: cfg,
		},
	})
	err := validateRecipeTunnelRefs(steps)
	if err == nil || !strings.Contains(err.Error(), "unknown step id") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateRecipeTunnelRefs_requiresTunnelKind(t *testing.T) {
	t.Parallel()
	cfg, _ := json.Marshal(map[string]string{"tunnel_step": "fetch"})
	steps := wrapAll(
		&CommandStep{StepBase: StepBase{ID: "fetch", Host: "*"}, Command: "echo"},
		&PluginStep{StepBase: StepBase{ID: "pg", Host: "*"}, Plugin: &RecipeStepPlugin{ID: "postgres", Action: "query", Config: cfg}},
	)
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
	if k := r.Steps[0].Step.Kind(); k != KindTunnel {
		t.Fatalf("kind=%v", k)
	}
}
