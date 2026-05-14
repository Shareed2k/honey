package cuetry

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func ptrBool(b bool) *bool { return &b }

func TestKVTunnelEnabled_defaultsAndStep(t *testing.T) {
	def := &RecipeDefaults{KVTunnel: ptrBool(true)}
	step := RecipeStep{}
	if !KVTunnelEnabled(step, def) {
		t.Fatal("expected enabled from defaults")
	}
	step = RecipeStep{KVTunnel: ptrBool(false)}
	if KVTunnelEnabled(step, def) {
		t.Fatal("step should override defaults off")
	}
}

func TestParseRemoteRecipe_kv_tunnel_ssh(t *testing.T) {
	cue := []byte(`
recipe: {
	name: "t"
	defaults: { kv_tunnel: true }
	steps: [{
		host: "*"
		command: "true"
		kv_tunnel: true
	}]
}
`)
	recs := []hosts.Record{
		{Name: "h1", Provider: "aws", PrimaryIP: "10.0.0.2"},
	}
	_, err := ParseRemoteRecipe(cue, recs)
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_kv_tunnel_k8sPod_ok(t *testing.T) {
	cue := []byte(`
recipe: {
	name: "t"
	defaults: { kv_tunnel: true }
	steps: [{
		host: "*"
		command: "true"
		kv_tunnel: true
	}]
}
`)
	recs := []hosts.Record{
		{Name: "p1", Provider: "k8s", Meta: map[string]string{"kind": "pod"}, PrimaryIP: "10.0.0.1"},
	}
	_, err := ParseRemoteRecipe(cue, recs)
	if err != nil {
		t.Fatal(err)
	}
}
