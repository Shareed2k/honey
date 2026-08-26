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

func TestBuildStepGraph_summarizeCannotBeDependedOn(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&SummarizeStep{StepBase: StepBase{ID: "summarize", Host: "_"}, Summarize: &RecipeSummarize{Prompt: "x"}},
		&CommandStep{StepBase: StepBase{ID: "bad", Host: "*", Depends: []string{"summarize"}}, Command: "true"},
	)
	_, err := BuildStepGraph(steps)
	if err == nil || !strings.Contains(err.Error(), "summarize") {
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
		{ id: "summarize", host: "_", depends: ["fetch"], summarize: { prompt: "ok" } },
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

func TestBuildStepGraph_rescue(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&CommandStep{StepBase: StepBase{ID: "fetch", Host: "*", Rescue: []string{"cleanup"}}, Command: "fetch"},
		&CommandStep{StepBase: StepBase{ID: "cleanup", Host: "*"}, Command: "cleanup"},
	)

	r := Recipe{
		Name:  "g",
		Type:  "graph",
		Steps: steps,
	}

	err := ValidateRecipeGraph(r)
	if err != nil {
		t.Fatalf("expected valid rescue, got %v", err)
	}

	stepsInvalid := wrapAll(
		&CommandStep{StepBase: StepBase{ID: "fetch", Host: "*", Rescue: []string{"unknown"}}, Command: "fetch"},
	)
	rInvalid := Recipe{
		Name:  "g",
		Type:  "graph",
		Steps: stepsInvalid,
	}
	err = ValidateRecipeGraph(rInvalid)
	if err == nil || !strings.Contains(err.Error(), "unknown step id") {
		t.Fatalf("expected unknown rescue step error, got %v", err)
	}
}

func TestBuildStepGraph_triggerRule(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&CommandStep{StepBase: StepBase{ID: "fetch", Host: "*"}, Command: "fetch"},
		&CommandStep{StepBase: StepBase{ID: "a", Host: "*", Depends: []string{"fetch"}, TriggerRule: "one_failed"}, Command: "a"},
		&CommandStep{StepBase: StepBase{ID: "b", Host: "*", Depends: []string{"fetch"}, TriggerRule: "all_done"}, Command: "b"},
		&CommandStep{StepBase: StepBase{ID: "invalid", Host: "*", Depends: []string{"fetch"}, TriggerRule: "unknown"}, Command: "v"},
	)

	// Since ValidateRecipeGraph tests this normally, let's create a recipe
	r := Recipe{
		Name:  "g",
		Type:  "graph",
		Steps: steps,
	}

	err := ValidateRecipeGraph(r)
	if err == nil || !strings.Contains(err.Error(), "invalid trigger_rule") {
		t.Fatalf("expected trigger_rule error, got %v", err)
	}
}

// TestBuildStepGraph_interceptSessionStep locks in the session_step auto-edge:
// a step reusing another intercept step's session must run after the
// establishing step, without the author writing an explicit depends. Mirrors
// the rescue-edge derivation above.
func TestBuildStepGraph_interceptSessionStep(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&InterceptStep{StepBase: StepBase{ID: "establish", Host: "*"}, Intercept: &RecipeStepIntercept{
			Targetless: true, Cluster: "staging", Namespace: "checkout", Command: "npm test",
		}},
		&InterceptStep{StepBase: StepBase{ID: "reuse", Host: "*"}, Intercept: &RecipeStepIntercept{
			SessionStep: "establish", Script: "run-suite.sh",
		}},
	)
	sg, err := BuildStepGraph(steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	establish, reuse := sg.IDToIndex["establish"], sg.IDToIndex["reuse"]
	if !containsInt(sg.Depends[reuse], establish) {
		t.Fatalf("expected reuse to depend on establish, got depends=%v", sg.Depends[reuse])
	}
	if !containsInt(sg.Children[establish], reuse) {
		t.Fatalf("expected establish to have reuse as a child, got children=%v", sg.Children[establish])
	}
}

func TestBuildStepGraph_interceptSessionStepUnknown(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&InterceptStep{StepBase: StepBase{ID: "reuse", Host: "*"}, Intercept: &RecipeStepIntercept{
			SessionStep: "missing", Script: "run-suite.sh",
		}},
	)
	_, err := BuildStepGraph(steps)
	if err == nil || !strings.Contains(err.Error(), "unknown step id") {
		t.Fatalf("expected unknown session_step id error, got %v", err)
	}
}

func TestBuildStepGraph_interceptSessionStepSelf(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&InterceptStep{StepBase: StepBase{ID: "establish", Host: "*"}, Intercept: &RecipeStepIntercept{
			SessionStep: "establish", Script: "run-suite.sh",
		}},
	)
	_, err := BuildStepGraph(steps)
	if err == nil || !strings.Contains(err.Error(), "must not reference itself") {
		t.Fatalf("expected self-reference error, got %v", err)
	}
}

func containsInt(haystack []int, needle int) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
