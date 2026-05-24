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
	want := 2 + 2*len(recs) // list_nodes, pre_verify, restart+verify per controller
	if got := len(r.Steps); got != want {
		t.Fatalf("steps: got %d want %d", got, want)
	}
	if r.Steps[0].ID != "list_nodes" || r.Steps[1].ID != "pre_verify" {
		t.Fatalf("first steps: %#v", r.Steps[:2])
	}
	if r.Steps[2].ID != "restart_0" || r.Steps[3].ID != "verify_0" {
		t.Fatalf("restart pair: %#v", r.Steps[2:4])
	}
	if r.Steps[len(r.Steps)-1].ID != "verify_2" {
		t.Fatalf("last step id: %s", r.Steps[len(r.Steps)-1].ID)
	}
	if r.Steps[2].Plugin == nil || r.Steps[2].Plugin.ID != "service" {
		t.Fatalf("restart_0 plugin: %#v", r.Steps[2].Plugin)
	}
	if r.Steps[1].Retry == nil || r.Steps[1].Retry.Attempts != 30 {
		t.Fatalf("pre_verify retry: %#v", r.Steps[1].Retry)
	}
	if r.Steps[3].Retry == nil || r.Steps[3].Retry.Attempts != 30 {
		t.Fatalf("verify_0 retry: %#v", r.Steps[3].Retry)
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
