package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	const yaml = `id: echo
version: "0.1.0"
capabilities:
  - cue_transform
  - custom_step
secret_ref_prefixes:
  - "echo:"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "echo" {
		t.Fatalf("id=%q", m.ID)
	}
	if !m.hasCapability(CapCueTransform) {
		t.Fatal("expected cue_transform capability")
	}
	if len(m.SecretRefPrefixes) != 1 || m.SecretRefPrefixes[0] != "echo:" {
		t.Fatalf("prefixes=%v", m.SecretRefPrefixes)
	}
}

func TestManagerDisabled(t *testing.T) {
	m, err := NewManager(t.Context(), PluginsFromConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	if m.Enabled() {
		t.Fatal("expected disabled")
	}
	if len(m.List()) != 0 {
		t.Fatalf("list=%v", m.List())
	}
	out, err := m.TransformCue(t.Context(), []byte("package x\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "package x\n" {
		t.Fatalf("transform changed bytes: %q", out)
	}
}
