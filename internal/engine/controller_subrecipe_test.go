package engine

import (
	"encoding/json"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestPromptsToToolSchema(t *testing.T) {
	raw := promptsToToolSchema(map[string]cuetry.RecipePrompt{
		"region": {Description: "cloud region", Required: true, Choices: []string{"eu", "us"}},
		"note":   {Description: "optional note"},
	})
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type        string   `json:"type"`
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v (%s)", err, raw)
	}
	if schema.Type != "object" {
		t.Errorf("type = %q, want object", schema.Type)
	}
	if schema.Properties["region"].Type != "string" || len(schema.Properties["region"].Enum) != 2 {
		t.Errorf("region property wrong: %+v", schema.Properties["region"])
	}
	if len(schema.Required) != 1 || schema.Required[0] != "region" {
		t.Errorf("required = %v, want [region]", schema.Required)
	}
}

func TestParseStepArgs(t *testing.T) {
	cases := map[string]struct {
		in   string
		want map[string]string
	}{
		"empty":              {"", nil},
		"object":             {"{}", nil},
		"strings + coercion": {`{"a":"x","n":2,"b":true,"z":null}`, map[string]string{"a": "x", "n": "2", "b": "true"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parseStepArgs(tc.in)
			if err != nil {
				t.Fatalf("parseStepArgs(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("key %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
	if _, err := parseStepArgs(`{bad json`); err == nil {
		t.Error("expected an error for invalid JSON args")
	}
}

func TestCloneRecipeStepWithPrompts(t *testing.T) {
	rs := &cuetry.RecipeStep{
		StepBase: cuetry.StepBase{ID: "deploy"},
		Recipe:   &cuetry.RecipeSubRecipe{Path: "sub.cue", Prompts: map[string]string{"a": "orig"}},
	}
	clone := cloneRecipeStepWithPrompts(rs, map[string]string{"a": "override", "b": "new"})
	cr, ok := clone.(*cuetry.RecipeStep)
	if !ok {
		t.Fatalf("clone is %T, want *cuetry.RecipeStep", clone)
	}
	if cr.Recipe.Prompts["a"] != "override" || cr.Recipe.Prompts["b"] != "new" {
		t.Errorf("clone prompts = %v, want a=override b=new", cr.Recipe.Prompts)
	}
	// Original must be untouched (steps may run again with different args).
	if rs.Recipe.Prompts["a"] != "orig" || len(rs.Recipe.Prompts) != 1 {
		t.Errorf("original prompts mutated: %v", rs.Recipe.Prompts)
	}
}
