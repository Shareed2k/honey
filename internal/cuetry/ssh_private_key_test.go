package cuetry

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestEffectiveSSHPrivateKey(t *testing.T) {
	t.Parallel()
	def := &RecipeDefaults{SSHPrivateKey: "~/.ssh/default"}
	step := RecipeStep{Host: "*", SSHPrivateKey: "~/.ssh/step"}
	if got := EffectiveSSHPrivateKey(def, step); got != "~/.ssh/step" {
		t.Fatalf("step over defaults: got %q", got)
	}
	if got := EffectiveSSHPrivateKey(def, RecipeStep{Host: "*"}); got != "~/.ssh/default" {
		t.Fatalf("defaults: got %q", got)
	}
	if got := EffectiveSSHPrivateKey(nil, RecipeStep{Host: "*"}); got != "" {
		t.Fatalf("unset: got %q", got)
	}
}

func TestRecordForSSHDial_identityMeta(t *testing.T) {
	t.Parallel()
	r := hosts.Record{Name: "a", PrimaryIP: "10.0.0.1"}
	out := RecordForSSHDial(&RecipeDefaults{SSHPrivateKey: "~/.ssh/k"}, RecipeStep{Host: "*"}, r)
	id, ok := hosts.MetaSSHIdentityFile(&out)
	if !ok || id != "~/.ssh/k" {
		t.Fatalf("identity meta got %q ok=%v", id, ok)
	}
	if r.Meta != nil {
		t.Fatal("mutated input record")
	}
}

func TestValidateSSHPrivateKeyField(t *testing.T) {
	t.Parallel()
	if err := validateSSHPrivateKeyField("steps[0]", "   "); err == nil {
		t.Fatal("expected error for whitespace-only")
	}
	if err := validateSSHPrivateKeyField("steps[0]", "~/.ssh/id"); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_sshPrivateKeyWhitespace(t *testing.T) {
	t.Parallel()
	_, err := ParseRemoteRecipe([]byte(`recipe: {
		name: "x"
		steps: [{ host: "*", ssh_private_key: "  ", command: "true" }]
	}`), nil)
	if err == nil || !strings.Contains(err.Error(), "ssh_private_key") {
		t.Fatalf("want ssh_private_key validation error, got %v", err)
	}
}
