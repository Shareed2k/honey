package cuetry

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
	"github.com/shareed2k/honey/internal/hosts"
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
	step := &StepBase{Env: map[string]string{"B": "9", "C": "3"}}
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
	step := &StepBase{Env: map[string]string{"B": "2"}}
	cli := map[string]string{"A": "9", "C": "3"}
	got, err := EffectiveEnvForRun(context.Background(), false, nil, step, def, cli, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "9" || got["B"] != "2" || got["C"] != "3" {
		t.Fatalf("%+v", got)
	}
}

func TestEffectiveEnvForRun_withHostRecord(t *testing.T) {
	step := &StepBase{Env: map[string]string{"USER_VAR": "custom"}}
	r := &hosts.Record{
		Name:      "web-01",
		PrimaryIP: "10.0.0.5",
		Provider:  "aws",
		Zone:      "us-east-1a",
		Region:    "us-east-1",
		Meta: map[string]string{
			"kind":     "instance",
			"bad-key@": "ignored",
		},
	}

	got, err := EffectiveEnvForRun(context.Background(), false, nil, step, nil, nil, r)
	if err != nil {
		t.Fatal(err)
	}

	expectedVars := map[string]string{
		"USER_VAR":              "custom",
		"HONEY_HOST_NAME":       "web-01",
		"HONEY_HOST_PRIMARY_IP": "10.0.0.5",
		"HONEY_HOST_PROVIDER":   "aws",
		"HONEY_HOST_ZONE":       "us-east-1a",
		"HONEY_HOST_REGION":     "us-east-1",
		"HONEY_HOST_META_KIND":  "instance",
	}

	for k, v := range expectedVars {
		if got[k] != v {
			t.Errorf("expected %s=%s, got %s", k, v, got[k])
		}
	}

	// bad-key@ becomes BAD_KEY_
	if got["HONEY_HOST_META_BAD_KEY_"] != "ignored" {
		t.Errorf("expected BAD_KEY_ to be present as sanitized format, got %s", got["HONEY_HOST_META_BAD_KEY_"])
	}
}

func TestEnvForDockerInteractive_omitsMetaLabels(t *testing.T) {
	r := hosts.Record{
		Provider:  "docker",
		Name:      "c1",
		PrimaryIP: "10.0.0.1",
		Meta: map[string]string{
			"kind":         "container",
			"container_id": "abc",
			"label_foo":    "bar",
		},
	}
	env, err := EnvForDockerInteractive(&r)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "HONEY_HOST_META_") {
			t.Fatalf("unexpected meta env %q", e)
		}
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

func TestEffectiveEnvForRun_resolvesSecureV1(t *testing.T) {
	key := make([]byte, stack.SymmetricKeyBytes)
	for i := range key {
		key[i] = byte(i + 1)
	}
	ref, err := stack.FormatSecureRef(key, "secret-value")
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewSecretResolver(SecretResolverOptions{SymmetricDataKey: key})
	if err != nil {
		t.Fatal(err)
	}
	step := &StepBase{Secrets: map[string]string{"TOKEN": ref}}
	got, err := EffectiveEnvForRun(context.Background(), true, res, step, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["TOKEN"] != "secret-value" {
		t.Fatalf("got %q", got["TOKEN"])
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
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Defaults == nil || r.Defaults.Env["GLOBAL"] != "g" {
		t.Fatalf("defaults: %+v", r.Defaults)
	}
	if r.Steps[0].Step.Base().Env["LOCAL"] != "l" {
		t.Fatalf("step env: %+v", r.Steps[0].Step.Base().Env)
	}
}
