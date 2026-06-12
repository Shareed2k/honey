package cuetry

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// BuildRecipeStepJSONSchema reflects each registered concrete step kind into its own
// definition, so every kind exposes exactly its own fields (e.g. template/ai have no
// ssh_port / max_parallel). The result is consumed by the RecipeStudio frontend.
func BuildRecipeStepJSONSchema() map[string]any {
	reflector := jsonschema.Reflector{ExpandedStruct: true}
	definitions := make(map[string]any)

	for _, inst := range reflectStepInstances() {
		definitions[inst.Kind] = reflectToMap(&reflector, inst.Step)
	}
	definitions["defaults"] = reflectToMap(&reflector, &RecipeDefaults{})

	return map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"type":        "object",
		"definitions": definitions,
	}
}

// reflectToMap reflects v into a JSON Schema and decodes it into a generic map.
// The schema is self-contained: nested types live under $defs and are referenced
// via $ref, which the frontend (recipeStudioUtils) resolves per kind.
func reflectToMap(reflector *jsonschema.Reflector, v any) map[string]any {
	schema := reflector.Reflect(v)
	b, _ := json.Marshal(schema)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
