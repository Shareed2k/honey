package cuetry

import (
	"fmt"
	"maps"
	"sort"
	"strings"
)

// PromptDefs returns the recipe's prompt definitions, or nil when unset.
// Safe to call on a zero-value Recipe.
func (r Recipe) PromptDefs() map[string]RecipePrompt {
	if r.Defaults == nil {
		return nil
	}
	return r.Defaults.Prompts
}

// ValidateAndApplyPromptDefaults merges prompt defaults into cliEnv and
// returns an error if any required prompt has neither a supplied value nor
// a default. Returns a new map; never mutates cliEnv.
func ValidateAndApplyPromptDefaults(prompts map[string]RecipePrompt, cliEnv map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(cliEnv)+len(prompts))
	maps.Copy(out, cliEnv)

	var missing []string
	for key, p := range prompts {
		if _, ok := out[key]; ok {
			continue // caller-supplied value wins
		}
		if p.Default != "" {
			out[key] = p.Default
		} else if p.Required {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("missing required prompt values: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
