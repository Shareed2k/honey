package cuetry

import (
	"testing"
)

func TestBuildRecipeStepJSONSchema(t *testing.T) {
	schema := BuildRecipeStepJSONSchema()

	if schema["type"] != "object" {
		t.Fatalf("expected type object")
	}

	defs, ok := schema["definitions"].(map[string]any)
	if !ok {
		t.Fatalf("expected definitions map")
	}

	if _, ok := defs["defaults"]; !ok {
		t.Fatalf("expected defaults in definitions")
	}
}
