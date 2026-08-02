package mobile

import (
	"encoding/json"
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

// TestStartMeshIfConfigured_NilOrDisabled proves the mesh auto-start helper
// never panics and is a true no-op for the common "no config yet" / "mesh not
// enabled" cases LoadConfig/InitDefaultConfig exercise on every phone that
// hasn't turned mesh on. Deliberately does not exercise cfg.Mesh.Enabled ==
// true: that path constructs a real libp2p Host, which this test suite must
// not depend on having network access for.
func TestStartMeshIfConfigured_NilOrDisabled(_ *testing.T) {
	startMeshIfConfigured(nil)
	startMeshIfConfigured(&config.File{})
}

// TestMeshStatus_ReportsErrorWhenNotStarted proves MeshStatus degrades to a
// plain {"error":...} JSON object (not a Go error) when meshnet was never
// started, so gomobile callers can always unconditionally JSON-decode the
// result without special-casing "not started".
func TestMeshStatus_ReportsErrorWhenNotStarted(t *testing.T) {
	got := MeshStatus()
	var out map[string]any
	if err := json.Unmarshal([]byte(got), &out); err != nil {
		t.Fatalf("MeshStatus did not return valid JSON: %v (%s)", err, got)
	}
	if _, ok := out["error"]; !ok {
		t.Errorf(`expected {"error":...} when mesh not started, got %s`, got)
	}
}
