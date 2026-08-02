package cuetry

import (
	"regexp"
	"sort"
	"testing"
)

// TestSchemaSource_coversGoStepFields guards the hand-maintained CUE #Step schema
// (schemaSource in recipe.go) against drifting behind the Go step structs. #Step
// is close({...}), so any Go step field with no matching CUE key is rejected as
// "unknown" before decode — a silent parse failure whenever someone adds a Go
// field and forgets the CUE side. Every user-input field reflected from the Go
// structs (the same reflection the frontend JSON schema uses) must appear as a
// key in schemaSource.
//
// When this fails: either add the field to schemaSource, or — if the field is
// intentionally not a user-input CUE key (computed/output-only) — add it to
// skipDriftFields below with a one-line reason.
func TestSchemaSource_coversGoStepFields(t *testing.T) {
	names := map[string]bool{}
	collectSchemaPropertyNames(BuildStepJSONSchema(), names)

	var missing []string
	for name := range names {
		if skipDriftFields[name] {
			continue
		}
		if !cueSchemaHasField(name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("Go step fields absent from the CUE #Step schema (schemaSource): %v\n"+
			"add each to schemaSource in recipe.go, or document it in skipDriftFields.", missing)
	}
}

// skipDriftFields lists reflected Go step fields that are intentionally absent
// from the user-input CUE schema. Each needs a reason.
var skipDriftFields = map[string]bool{
	// RecipeSecret / RecipeSecretVault / *Aws / *Gcp backend fields. Recipe
	// secrets are string refs in CUE (`secrets?: {[string]: string}`); this
	// structured form is the *parsed* representation, never a CUE input key.
	"vault":     true,
	"aws":       true,
	"gcp":       true,
	"key":       true,
	"project":   true,
	"secret":    true,
	"secret_id": true,
	"version":   true,
}

// collectSchemaPropertyNames walks a JSON Schema map recursively, collecting every
// key that appears under a "properties" object (i.e. every reflected field name).
func collectSchemaPropertyNames(node any, out map[string]bool) {
	switch v := node.(type) {
	case map[string]any:
		for k, val := range v {
			if k == "properties" {
				if props, ok := val.(map[string]any); ok {
					for name := range props {
						out[name] = true
					}
				}
			}
			collectSchemaPropertyNames(val, out)
		}
	case []any:
		for _, e := range v {
			collectSchemaPropertyNames(e, out)
		}
	}
}

// cueSchemaHasField reports whether schemaSource declares a field named name.
// CUE fields look like `name:` or `name?:`, and keywords are quoted (`"for":`),
// so the name may be wrapped in quotes; a preceding non-word char anchors the
// match so e.g. "secret" does not match inside "secret_id".
func cueSchemaHasField(name string) bool {
	re := regexp.MustCompile(`(?:^|[^\w])"?` + regexp.QuoteMeta(name) + `"?\s*\??\s*:`)
	return re.MatchString(schemaSource)
}
