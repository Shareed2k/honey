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

	// Each registered kind gets its own self-contained definition.
	for _, kind := range []string{KindCommand, KindTemplate, KindPostgres, KindAI} {
		def, ok := defs[kind].(map[string]any)
		if !ok {
			t.Fatalf("expected definition for kind %q", kind)
		}
		if _, ok := def["properties"]; !ok {
			t.Fatalf("kind %q definition missing properties", kind)
		}
	}

	// Template/ai are local: their schema must NOT expose remote SSH/fan-out fields.
	for _, kind := range []string{KindTemplate, KindAI} {
		def := defs[kind].(map[string]any)
		props := def["properties"].(map[string]any)
		for _, banned := range []string{"ssh_port", "max_parallel", "serial", "ssh_private_key"} {
			if _, bad := props[banned]; bad {
				t.Fatalf("kind %q must not expose %q", kind, banned)
			}
		}
	}
}
