package cuetry

import (
	"strings"
	"testing"
)

func TestValidateRecipeEnvMap_badKey(t *testing.T) {
	if err := ValidateRecipeEnvMap(map[string]string{"1BAD": "x"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRecipeEnvMap_badValue(t *testing.T) {
	if err := ValidateRecipeEnvMap(map[string]string{"OK": "a\nb"}); err == nil {
		t.Fatal("expected error")
	}
}

func TestEffectiveEnv_stepOverridesDefaults(t *testing.T) {
	def := &RecipeDefaults{Env: map[string]string{"A": "1", "B": "2"}}
	step := RecipeStep{Env: map[string]string{"B": "9", "C": "3"}}
	got, err := EffectiveEnv(step, def)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "1" || got["B"] != "9" || got["C"] != "3" {
		t.Fatalf("%+v", got)
	}
}

func TestParseEnvKeyValuePairs(t *testing.T) {
	got, err := ParseEnvKeyValuePairs([]string{"A=1", " B=2 "})
	if err != nil || got["A"] != "1" || strings.TrimSpace(got["B"]) != "2" {
		t.Fatalf("%+v %v", got, err)
	}
	_, err = ParseEnvKeyValuePairs([]string{"noequals"})
	if err == nil {
		t.Fatal("expected error")
	}
	got2, err := ParseEnvKeyValuePairs([]string{"X=a=b"})
	if err != nil || got2["X"] != "a=b" {
		t.Fatalf("%+v %v", got2, err)
	}
}

func TestEffectiveEnvForRun_cliOverrides(t *testing.T) {
	def := &RecipeDefaults{Env: map[string]string{"A": "1"}}
	step := RecipeStep{Env: map[string]string{"B": "2"}}
	cli := map[string]string{"A": "9", "C": "3"}
	got, err := EffectiveEnvForRun(step, def, cli)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "9" || got["B"] != "2" || got["C"] != "3" {
		t.Fatalf("%+v", got)
	}
}

func TestShellExportPrefixForRemote_sortsKeys(t *testing.T) {
	got, err := ShellExportPrefixForRemote(
		map[string]string{"Z": "1", "A": "2"},
		"echo ok",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "export A='2'; export Z='1'; echo ok") {
		t.Fatalf("%q", got)
	}
}

func TestParseRemoteRecipe_putWithEnvRejected(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{host: "10.0.0.1", put: {local: "./x", remote: "/tmp/x"}, env: {FOO: "bar"}},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRemoteRecipe_defaultsAndStepEnv(t *testing.T) {
	const src = `
recipe: {
	name: "env"
	defaults: { env: { GLOBAL: "g" } }
	steps: [
		{host: "10.0.0.1", command: "id", env: { LOCAL: "l" }},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if r.Defaults == nil || r.Defaults.Env["GLOBAL"] != "g" {
		t.Fatalf("defaults: %+v", r.Defaults)
	}
	if r.Steps[0].Env["LOCAL"] != "l" {
		t.Fatalf("step env: %+v", r.Steps[0].Env)
	}
}
