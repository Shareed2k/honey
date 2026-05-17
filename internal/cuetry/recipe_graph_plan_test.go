package cuetry

import (
	"strings"
	"testing"
)

func TestBuildRecipeGraphPlan(t *testing.T) {
	t.Parallel()
	r := Recipe{
		Name: "g",
		Type: "graph",
		Steps: []RecipeStep{
			{ID: "fetch", Host: "*", Command: "f"},
			{ID: "a", Host: "*", Depends: []string{"fetch"}, Command: "a"},
		},
	}
	plan, err := BuildRecipeGraphPlan(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 2 || len(plan.Edges) != 1 {
		t.Fatalf("nodes=%d edges=%d", len(plan.Nodes), len(plan.Edges))
	}
	if !strings.Contains(plan.Mermaid, "flowchart") {
		t.Fatalf("mermaid: %q", plan.Mermaid)
	}
}

func TestBuildRecipeGraphPlan_rejectsLinear(t *testing.T) {
	t.Parallel()
	_, err := BuildRecipeGraphPlan(Recipe{Name: "l", Steps: []RecipeStep{{Host: "*", Command: "x"}}})
	if err == nil || !strings.Contains(err.Error(), "graph") {
		t.Fatalf("got %v", err)
	}
}
