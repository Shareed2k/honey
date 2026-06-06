package cuetry

import (
	"encoding/json"

	"github.com/invopop/jsonschema"
)

// BuildRecipeStepJSONSchema dynamically reflects the RecipeStep structure to compile a standard JSON Schema.
func BuildRecipeStepJSONSchema() map[string]any {
	reflector := jsonschema.Reflector{ExpandedStruct: true}
	schema := reflector.Reflect(&RecipeStep{})
	defaultsSchema := reflector.Reflect(&RecipeDefaults{})

	b, _ := json.Marshal(schema)
	var m map[string]any
	_ = json.Unmarshal(b, &m)

	definitions := make(map[string]any)

	// Extract $defs map from the decoded map m
	if d, ok := m["$defs"]; ok {
		if dm, ok2 := d.(map[string]any); ok2 {
			for k, v := range dm {
				definitions[k] = v
			}
		}
	}

	bDefs, _ := json.Marshal(defaultsSchema)
	var mDefs map[string]any
	_ = json.Unmarshal(bDefs, &mDefs)
	definitions["defaults"] = mDefs

	m["definitions"] = definitions
	delete(m, "$defs")
	return m
}

// JSONSchemaExtend is an empty jsonschema modifier required to bypass static checks.
func (RecipeStep) JSONSchemaExtend(_ *jsonschema.Schema) {
	// Instead of a giant struct with all fields, we instruct the UI to use it exactly as is!
	// Wait, the user specifically wanted to "just convert internal/cuetry/recipe_types.go to jsonschema and use it for form, whitouch static checks and duplication code".
	// The implementation below doesn't manipulate `oneOf` since the user rejected my proposal for that. We just return the flat schema directly.
}
