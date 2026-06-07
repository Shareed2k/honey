package cuetry

import (
	"strings"
	"testing"
)

func TestValidateRecipeGraph_linearRejectsDepends(t *testing.T) {
	t.Parallel()
	r := Recipe{
		Name: "t",
		Steps: wrapAll(&CommandStep{
			StepBase: StepBase{Host: "*", Depends: []string{"x"}},
			Command:  "true",
		}),
	}
	if err := ValidateRecipeGraph(r); err == nil || !strings.Contains(err.Error(), "depends") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildStepGraph_cycle(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&CommandStep{StepBase: StepBase{ID: "a", Host: "*", Depends: []string{"b"}}, Command: "a"},
		&CommandStep{StepBase: StepBase{ID: "b", Host: "*", Depends: []string{"a"}}, Command: "b"},
	)
	_, err := BuildStepGraph(steps)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildStepGraph_waves(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&CommandStep{StepBase: StepBase{ID: "fetch", Host: "*"}, Command: "fetch"},
		&CommandStep{StepBase: StepBase{ID: "a", Host: "*", Depends: []string{"fetch"}}, Command: "a"},
		&CommandStep{StepBase: StepBase{ID: "b", Host: "*", Depends: []string{"fetch"}}, Command: "b"},
		&CommandStep{StepBase: StepBase{ID: "verify", Host: "*", Depends: []string{"a", "b"}}, Command: "v"},
	)
	sg, err := BuildStepGraph(steps)
	if err != nil {
		t.Fatal(err)
	}
	if len(sg.Waves) != 3 {
		t.Fatalf("waves: %d", len(sg.Waves))
	}
	if len(sg.Waves[1]) != 2 {
		t.Fatalf("wave 2 should have 2 parallel steps, got %v", sg.Waves[1])
	}
}

func TestBuildStepGraph_aiCannotBeDependedOn(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&AIStep{StepBase: StepBase{ID: "ai", Host: "_"}, AI: &RecipeAI{Prompt: "x"}},
		&CommandStep{StepBase: StepBase{ID: "bad", Host: "*", Depends: []string{"ai"}}, Command: "true"},
	)
	_, err := BuildStepGraph(steps)
	if err == nil || !strings.Contains(err.Error(), "ai") {
		t.Fatalf("got %v", err)
	}
}

func TestParseRemoteRecipe_graphMode(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "g"
	type: "graph"
	steps: [
		{ id: "fetch", host: "*", command: "echo" },
		{ id: "summarize", host: "_", depends: ["fetch"], ai: { prompt: "ok" } },
	]
}
`
	r, err := ParseRemoteRecipe([]byte(cue), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Type != "graph" || r.Steps[0].Step.Base().ID != "fetch" {
		t.Fatalf("%+v", r)
	}
}

func TestParseRemoteRecipe_graphAllowsMultiKVTunnel(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "g"
	type: "graph"
	defaults: { kv_tunnel: true }
	steps: [
		{ id: "a", host: "*", command: "one" },
		{ id: "b", host: "*", depends: ["a"], command: "two" },
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(cue), nil); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_graphKVTunnelRequiresID(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "g"
	type: "graph"
	steps: [
		{ host: "*", kv_tunnel: true, command: "one" },
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("got %v", err)
	}
}

func TestFormatGraphWavesText(t *testing.T) {
	t.Parallel()
	r := Recipe{
		Name: "g",
		Type: "graph",
		Steps: wrapAll(
			&CommandStep{StepBase: StepBase{ID: "fetch", Host: "*"}, Command: "f"},
			&CommandStep{StepBase: StepBase{ID: "a", Host: "*", Depends: []string{"fetch"}}, Command: "a"},
		),
	}
	text, err := FormatGraphWavesText(r)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "wave 1") || !strings.Contains(text, "fetch") {
		t.Fatalf("%q", text)
	}
}
