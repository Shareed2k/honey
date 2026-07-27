package cuetry

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestEffectiveSSHPrivateKey(t *testing.T) {
	t.Parallel()
	def := &RecipeDefaults{SSHPrivateKey: "~/.ssh/default"}
	step := &RemoteExec{SSHPrivateKey: "~/.ssh/step"}
	if got := EffectiveSSHPrivateKey(def, step); got != "~/.ssh/step" {
		t.Fatalf("step over defaults: got %q", got)
	}
	if got := EffectiveSSHPrivateKey(def, &RemoteExec{}); got != "~/.ssh/default" {
		t.Fatalf("defaults: got %q", got)
	}
	if got := EffectiveSSHPrivateKey(nil, &RemoteExec{}); got != "" {
		t.Fatalf("unset: got %q", got)
	}
}

func TestRecordForSSHDial_identityMeta(t *testing.T) {
	t.Parallel()
	r := hosts.Record{Name: "a", PrimaryIP: "10.0.0.1"}
	out := RecordForSSHDial(&RecipeDefaults{SSHPrivateKey: "~/.ssh/k"}, &RemoteExec{}, r)
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

// TestValidateParsedRecipe_sshPrivateKeyWhitespace guards against the JSON exec
// path drifting laxer than the CUE path: both go through validateRecipeSemantics,
// so ValidateParsedRecipe must reject a whitespace-only defaults.ssh_private_key
// exactly as ParseRemoteRecipe does. Before the two validators were unified, the
// JSON path silently skipped the defaults ssh_private_key and retry checks.
func TestValidateParsedRecipe_sshPrivateKeyWhitespace(t *testing.T) {
	t.Parallel()
	r := Recipe{
		Name:     "x",
		Defaults: &RecipeDefaults{SSHPrivateKey: "  "},
		Steps:    wrapAll(&CommandStep{StepBase: StepBase{Host: "*"}, Command: "true"}),
	}
	if err := ValidateParsedRecipe(r, nil); err == nil || !strings.Contains(err.Error(), "ssh_private_key") {
		t.Fatalf("JSON path must reject whitespace defaults.ssh_private_key like the CUE path; got %v", err)
	}
}
