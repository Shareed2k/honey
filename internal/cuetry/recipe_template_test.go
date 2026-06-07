package cuetry

import (
	"strings"
	"testing"
)

func TestClassifyStep_template(t *testing.T) {
	t.Parallel()
	step := &TemplateStep{
		StepBase: StepBase{Host: MatchLocalAIHost},
		Template: &RecipeStepTemplate{Template: "x"},
	}
	if step.Kind() != KindTemplate {
		t.Fatalf("kind=%v", step.Kind())
	}
}

func TestParseRemoteRecipe_graphTemplateFromOutput(t *testing.T) {
	t.Parallel()
	cue := `
recipe: {
	name: "tpl"
	type: "graph"
	steps: [
		{ id: "fetch", host: "*", command: "hostname" },
		{
			id: "render"
			host: "_"
			depends: ["fetch"]
			env_from: [{ step: "fetch", map: { HOSTNAME: "stdout" } }]
			template: {
				template: "host={{ .HOSTNAME }}\n"
				output: "RESULT"
			}
		},
		{
			id: "use"
			host: "*"
			depends: ["render"]
			env_from: [{ from_output: "RESULT", map: { CFG: "stdout" } }]
			command: "echo \"$CFG\""
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(cue), nil); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnvFromRefs_rejectsBothStepAndFromOutput(t *testing.T) {
	t.Parallel()
	steps := wrapAll(
		&TemplateStep{StepBase: StepBase{ID: "a", Host: "_"}, Template: &RecipeStepTemplate{Template: "x", Output: "OUT"}},
		&CommandStep{StepBase: StepBase{ID: "b", Host: "*", Depends: []string{"a"}, EnvFrom: []EnvFromRef{{
			Step: "a", FromOutput: "OUT", Map: map[string]string{"X": "stdout"},
		}}}, Command: "echo"},
	)
	sg, err := BuildStepGraph(steps)
	if err != nil {
		t.Fatal(err)
	}
	err = validateEnvFromRefs(1, steps[1].Step.Base(), sg, templateOutputProducers(steps))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("got %v", err)
	}
}

func TestMergeEnvFromInto_fromOutput(t *testing.T) {
	t.Parallel()
	outputCap := NewRecipeOutputCapture()
	outputCap.Set("RESULT", "hello")
	step := &StepBase{
		EnvFrom: []EnvFromRef{{
			FromOutput: "RESULT",
			Map:        map[string]string{"CFG": "stdout"},
		}},
	}
	dst := map[string]string{}
	if err := MergeEnvFromInto(dst, step, nil, outputCap, nil, "", false); err != nil {
		t.Fatal(err)
	}
	if dst["CFG"] != "hello" {
		t.Fatalf("%v", dst)
	}
}

func TestValidateStepTemplate_outputRequiresLocalHost(t *testing.T) {
	t.Parallel()
	err := validateDecodedRecipeStep(StepValidateCtx{Index: 0, NumSteps: 1, Mode: ExecutionModeLinear}, wrap(&TemplateStep{
		StepBase: StepBase{Host: "*"},
		Template: &RecipeStepTemplate{Template: "x", Output: "RESULT"},
	}))
	if err == nil || !strings.Contains(err.Error(), "template.output requires host") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateStepTemplate_allowsPerHostWithoutOutput(t *testing.T) {
	t.Parallel()
	err := validateDecodedRecipeStep(StepValidateCtx{Index: 0, NumSteps: 1, Mode: ExecutionModeLinear}, wrap(&TemplateStep{
		StepBase: StepBase{Host: "*"},
		Template: &RecipeStepTemplate{Template: "x"},
	}))
	if err != nil {
		t.Fatalf("got %v", err)
	}
}
