package cuetry

import (
	"encoding/json"
	"testing"
)

func TestExpandMatrixSteps(t *testing.T) {
	recipeJSON := `{
		"name": "test",
		"type": "graph",
		"steps": [
			{
				"id": "setup",
				"command": "echo setup"
			},
			{
				"id": "work",
				"depends": ["setup"],
				"matrix": {"os": ["linux", "darwin"], "arch": ["amd64"]},
				"command": "echo work"
			},
			{
				"id": "cleanup",
				"depends": ["work"],
				"command": "echo cleanup"
			}
		]
	}`

	var r Recipe
	if err := json.Unmarshal([]byte(recipeJSON), &r); err != nil {
		t.Fatal(err)
	}

	if err := ExpandMatrixSteps(&r); err != nil {
		t.Fatal(err)
	}

	if len(r.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(r.Steps))
	}

	hasWorkLinux := false
	for _, w := range r.Steps {
		if w.Step.Base().ID == "work[arch=amd64,os=linux]" {
			hasWorkLinux = true
			if w.Step.Base().Env["os"] != "linux" || w.Step.Base().Env["arch"] != "amd64" {
				t.Errorf("missing or incorrect env on expanded step: %v", w.Step.Base().Env)
			}
			if len(w.Step.Base().Depends) != 1 || w.Step.Base().Depends[0] != "setup" {
				t.Errorf("incorrect depends on expanded step: %v", w.Step.Base().Depends)
			}
		}
		if w.Step.Base().ID == "cleanup" {
			if len(w.Step.Base().Depends) != 2 {
				t.Errorf("cleanup should depend on 2 expanded nodes, got: %v", w.Step.Base().Depends)
			}
		}
	}
	if !hasWorkLinux {
		t.Errorf("missing expected expanded node work[arch=amd64,os=linux]")
	}
}
