package cuetry

import "testing"

func TestKVTunnelEnabled_alwaysTrue(t *testing.T) {
	t.Parallel()
	step := &CommandStep{StepBase: StepBase{KVTunnel: ptrBool(false)}, Command: "x"}
	def := &RecipeDefaults{KVTunnel: ptrBool(false)}
	if !KVTunnelEnabled(step, def) {
		t.Fatal("expected kv_tunnel always enabled")
	}
}

func ptrBool(v bool) *bool { return &v }
