package cuetry

import (
	"strings"
	"testing"
)

func TestStepOutputStore_recordGet(t *testing.T) {
	t.Parallel()
	s := NewStepOutputStore()
	s.Record("fetch", "web-1", "  hello  ")
	v, ok := s.Get("fetch", "web-1")
	if !ok || v != "hello" {
		t.Fatalf("got %q ok=%v", v, ok)
	}
}

func TestMergeEnvFromInto_dryRun(t *testing.T) {
	t.Parallel()
	step := RecipeStep{
		EnvFrom: []EnvFromRef{{
			Step: "fetch",
			Map:  map[string]string{"TAG": "stdout"},
		}},
	}
	dst := map[string]string{}
	if err := MergeEnvFromInto(dst, step, nil, nil, nil, "h1", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dst["TAG"], "fetch") {
		t.Fatalf("%v", dst)
	}
}

func TestMergeEnvFromInto_missingStdout(t *testing.T) {
	t.Parallel()
	step := RecipeStep{
		EnvFrom: []EnvFromRef{{
			Step: "fetch",
			Map:  map[string]string{"TAG": "stdout"},
		}},
	}
	err := MergeEnvFromInto(map[string]string{}, step, NewStepOutputStore(), nil, nil, "h1", false)
	if err == nil || !strings.Contains(err.Error(), "no stdout") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateEnvFromRefs_requiresDepends(t *testing.T) {
	t.Parallel()
	steps := []RecipeStep{
		{ID: "fetch", Host: "*", Command: "echo"},
		{ID: "use", Host: "*", Depends: []string{"fetch"}, Command: "echo", EnvFrom: []EnvFromRef{{
			Step: "fetch",
			Map:  map[string]string{"X": "stdout"},
		}}},
	}
	sg, err := BuildStepGraph(steps)
	if err != nil {
		t.Fatal(err)
	}
	producers := templateOutputProducers(steps)
	if err := validateEnvFromRefs(1, steps[1], sg, producers); err != nil {
		t.Fatal(err)
	}
	steps[1].Depends = nil
	if err := validateEnvFromRefs(1, steps[1], sg, producers); err == nil {
		t.Fatal("expected depends error")
	}
}

func TestParseRemoteRecipe_graphEnvFrom(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "g"
	type: "graph"
	steps: [
		{ id: "fetch", host: "*", command: "echo" },
		{ id: "use", host: "*", depends: ["fetch"], env_from: [{ step: "fetch", map: { TAG: "stdout" } }], command: "echo $TAG" },
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(cue), nil); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_linearRejectsEnvFrom(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "l"
	steps: [{ host: "*", command: "x", env_from: [{ step: "a", map: { X: "stdout" } }] }]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "env_from") {
		t.Fatalf("got %v", err)
	}
}
