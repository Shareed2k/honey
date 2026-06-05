package cuetry

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestParseKafkaControllerRollingRestart(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "examples", "recipe", "kafka_controller_rolling_restart.cue")
	b, err := os.ReadFile(root)
	if err != nil {
		t.Fatal(err)
	}
	recs := []hosts.Record{
		{Name: "kafka-ctrl-1", PrimaryIP: "10.0.0.1", Provider: "consul"},
		{Name: "kafka-ctrl-2", PrimaryIP: "10.0.0.2", Provider: "consul"},
		{Name: "kafka-ctrl-3", PrimaryIP: "10.0.0.3", Provider: "consul"},
	}
	r, err := ParseRemoteRecipe(b, recs)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.Steps); got != 3 {
		t.Fatalf("steps: got %d want 3", got)
	}
	if r.Steps[0].ID != "list_nodes" || r.Steps[1].ID != "verify_cluster_health" {
		t.Fatalf("first steps: %#v", r.Steps[:2])
	}
	if r.Steps[0].Output != "controllers_raw" {
		t.Fatalf("list output: %#v", r.Steps[0])
	}
	restart := r.Steps[2]
	if restart.ID != "restart" || restart.Loop == "" || restart.Host != "${item}" || restart.Serial != 1 {
		t.Fatalf("restart step: %#v", restart)
	}
	if restart.Hooks == nil || restart.Hooks.OnSuccess == nil {
		t.Fatalf("restart hook: %#v", restart.Hooks)
	}
	if restart.Hooks.OnSuccess.Where != "" || restart.Hooks.OnSuccess.Command != "quorum-cli am-i-caught-up" {
		t.Fatalf("restart hook: %#v", restart.Hooks.OnSuccess)
	}
}

func TestParseKafkaControllerHostsIndex(t *testing.T) {
	t.Parallel()
	src := []byte(`
recipe: {
  name: "test"
  type: "graph"
  steps: [
    { id: "a", host: hosts[0].name, command: "echo" },
  ]
}`)
	recs := []hosts.Record{{Name: "kafka-ctrl-1", PrimaryIP: "10.0.0.1", Provider: "consul"}}
	r, err := ParseRemoteRecipe(src, recs)
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Host != "kafka-ctrl-1" {
		t.Fatalf("host: %q", r.Steps[0].Host)
	}
}
