package ui

import (
	"encoding/json"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestRecipeMeta_IncludesPlanAndGraph(t *testing.T) {
	meta := RecipeMeta{
		Plan: "test plan",
		Graph: &cuetry.RecipeGraphPlan{
			Nodes: []cuetry.GraphPlanNode{{ID: "step1"}},
		},
	}

	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded RecipeMeta
	err = json.Unmarshal(b, &decoded)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Plan != "test plan" {
		t.Errorf("expected plan 'test plan', got '%s'", decoded.Plan)
	}
	if decoded.Graph == nil || len(decoded.Graph.Nodes) != 1 {
		t.Errorf("expected graph to be serialized and decoded")
	}
}
