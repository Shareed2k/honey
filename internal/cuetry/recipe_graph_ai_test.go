package cuetry

import "testing"

func TestBuildStepGraph_aiCanBeDependedOn(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&AIStep{StepBase: StepBase{ID: "ask", Host: "_"}, AI: &RecipeAI{Prompt: "x"}},
		&CommandStep{StepBase: StepBase{ID: "use-it", Host: "*", Depends: []string{"ask"}}, Command: "true"},
	)
	if _, err := BuildStepGraph(steps); err != nil {
		t.Fatalf("expected a downstream step to be allowed to depend on ai:, got: %v", err)
	}
}

func TestBuildStepGraph_multipleAISteps(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&AIStep{StepBase: StepBase{ID: "ask1", Host: "_"}, AI: &RecipeAI{Prompt: "x"}},
		&AIStep{StepBase: StepBase{ID: "ask2", Host: "_"}, AI: &RecipeAI{Prompt: "y"}},
	)
	if _, err := BuildStepGraph(steps); err != nil {
		t.Fatalf("expected more than one ai: step to be allowed, got: %v", err)
	}
}

func TestParseRemoteRecipe_aiOutputConsumedByEnvFrom(t *testing.T) {
	cue := `
recipe: {
	name: "t"
	type: "graph"
	steps: [
		{
			id:     "ask"
			host:   "_"
			output: "ANSWER"
			ai: { prompt: "What is 2+2?" }
		},
		{
			id:       "use-it"
			host:     "*"
			depends:  ["ask"]
			env_from: [{ from_output: "ANSWER", map: { A: "stdout" } }]
			command:  "echo $A"
		},
	]
}
`
	_, err := ParseRemoteRecipe([]byte(cue), nil)
	if err != nil {
		t.Fatalf("expected a downstream step to consume ai:'s output via env_from/from_output, got: %v", err)
	}
}
