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

func TestValidateStepTunnel_udpWithoutSocatIsServerBridge(t *testing.T) {
	t.Parallel()
	// mode:"udp" without remote_socat is valid: it selects the non-socat
	// vantage (honeyprovider's server-side Go UDP bridge, useSocat=false),
	// rather than being rejected.
	err := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel: &RecipeStepTunnel{
			Mode:       "udp",
			RemotePort: 53,
		},
	}).Validate(StepValidateCtx{Index: 0, Mode: ExecutionModeLinear})
	if err != nil {
		t.Fatalf("expected udp without remote_socat to be valid, got %v", err)
	}
}

func TestValidateStepTunnel_udpWithSocatIsValid(t *testing.T) {
	t.Parallel()
	// remote_socat:true still selects socat-on-target and remains valid.
	err := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel: &RecipeStepTunnel{
			Mode:        "udp",
			RemotePort:  53,
			RemoteSocat: true,
		},
	}).Validate(StepValidateCtx{Index: 0, Mode: ExecutionModeLinear})
	if err != nil {
		t.Fatalf("expected udp with remote_socat to be valid, got %v", err)
	}
}

func TestValidateStepTunnel_unixMode(t *testing.T) {
	t.Parallel()
	vc := StepValidateCtx{Index: 0, Mode: ExecutionModeLinear}

	ok := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel:   &RecipeStepTunnel{Mode: "unix", RemoteSocket: "/var/run/postgresql/.s.PGSQL.5432"},
	}).Validate(vc)
	if ok != nil {
		t.Fatalf("valid unix tunnel rejected: %v", ok)
	}

	if err := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel:   &RecipeStepTunnel{Mode: "unix"},
	}).Validate(vc); err == nil || !strings.Contains(err.Error(), "remote_socket") {
		t.Fatalf("unix without remote_socket must fail: %v", err)
	}

	if err := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel:   &RecipeStepTunnel{Mode: "unix", RemoteSocket: "run/pg.sock"},
	}).Validate(vc); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative remote_socket must fail: %v", err)
	}

	if err := (&TunnelStep{
		StepBase: StepBase{Host: "db-*"},
		Tunnel:   &RecipeStepTunnel{Mode: "unix", RemoteSocket: "/s.sock", RemotePort: 5432},
	}).Validate(vc); err == nil || !strings.Contains(err.Error(), "tcp port fields") {
		t.Fatalf("unix with remote_port must fail: %v", err)
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
