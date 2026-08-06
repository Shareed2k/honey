package engine

import (
	"encoding/json"
	"fmt"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
)

// loadControllerStepPrompts pre-loads, for each sub-recipe (`recipe:`) step, the
// callee's declared `defaults.prompts` — the source for that step's tool
// parameters (so the LLM can fill them). Best-effort: a callee that can't be read
// or parsed here is simply exposed as a no-argument tool; the real error surfaces
// if/when the LLM actually runs it.
func loadControllerStepPrompts(recipe cuetry.Recipe, recipeDir string) map[int]map[string]cuetry.RecipePrompt {
	out := make(map[int]map[string]cuetry.RecipePrompt)
	for i, ws := range recipe.Steps {
		rs, ok := ws.Step.(*cuetry.RecipeStep)
		if !ok || rs.Recipe == nil || rs.Recipe.Path == "" {
			continue
		}
		p, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, rs.Recipe.Path)
		if err != nil {
			continue
		}
		b, err := safepath.ReadFile(p)
		if err != nil {
			continue
		}
		sub, err := cuetry.ParseRemoteRecipe(b, nil)
		if err != nil {
			continue
		}
		if sub.Defaults != nil && len(sub.Defaults.Prompts) > 0 {
			out[i] = sub.Defaults.Prompts
		}
	}
	return out
}

// promptsToToolSchema renders a sub-recipe's declared prompts as a JSON Schema
// for the step tool's parameters. Every prompt is a string argument (sub-recipe
// prompt values are strings); `choices` become an enum and `required` prompts the
// schema's required list.
func promptsToToolSchema(prompts map[string]cuetry.RecipePrompt) json.RawMessage {
	properties := make(map[string]any, len(prompts))
	var required []string
	for name, p := range prompts {
		prop := map[string]any{"type": "string"}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		if len(p.Choices) > 0 {
			prop["enum"] = p.Choices
		}
		properties[name] = prop
		if p.Required {
			required = append(required, name)
		}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	return b
}

// parseStepArgs decodes a tool call's JSON arguments into string prompt values.
// Numbers/bools are stringified (sub-recipe prompts are string→string). An empty
// argument string yields a nil map (a no-argument step tool).
func parseStepArgs(raw string) (map[string]string, error) {
	if raw == "" || raw == "{}" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("invalid tool arguments: %w", err)
	}
	if len(m) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			// omit null values
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out, nil
}

// cloneRecipeStepWithPrompts returns a copy of a sub-recipe step with the LLM's
// arguments merged into its prompts (author-set prompts are the base; args
// override). The original step is not mutated (controller steps may run more than
// once with different args).
func cloneRecipeStepWithPrompts(rs *cuetry.RecipeStep, args map[string]string) cuetry.Step {
	clone, ok := rs.Clone().(*cuetry.RecipeStep) // deep-copies StepBase; Recipe pointer shared
	if !ok {
		return rs
	}
	sub := cuetry.RecipeSubRecipe{}
	if rs.Recipe != nil {
		sub = *rs.Recipe
	}
	merged := make(map[string]string, len(sub.Prompts)+len(args))
	for k, v := range sub.Prompts {
		merged[k] = v
	}
	for k, v := range args {
		merged[k] = v
	}
	sub.Prompts = merged
	clone.Recipe = &sub
	return clone
}
