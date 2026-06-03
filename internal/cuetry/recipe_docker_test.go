package cuetry

import (
	"testing"
)

func TestClassifyDockerStep(t *testing.T) {
	step := RecipeStep{
		Host: "localhost",
		Docker: &RecipeStepDocker{
			Action: "build",
			Build: &DockerBuild{
				Context: "./app",
			},
		},
	}
	kind, err := ClassifyStep(step)
	if err != nil {
		t.Fatalf("unexpected classification error: %v", err)
	}
	if kind != StepKindDocker {
		t.Errorf("expected StepKindDocker, got %v", kind)
	}
}
