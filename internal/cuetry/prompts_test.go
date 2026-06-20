package cuetry_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestValidateAndApplyPromptDefaults_RequiredMissing(t *testing.T) {
	prompts := map[string]cuetry.RecipePrompt{
		"host": {Required: true},
	}
	_, err := cuetry.ValidateAndApplyPromptDefaults(prompts, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "host")
}

func TestValidateAndApplyPromptDefaults_RequiredWithDefault(t *testing.T) {
	prompts := map[string]cuetry.RecipePrompt{
		"env": {Required: true, Default: "staging"},
	}
	out, err := cuetry.ValidateAndApplyPromptDefaults(prompts, nil)
	require.NoError(t, err)
	require.Equal(t, "staging", out["env"])
}

func TestValidateAndApplyPromptDefaults_RequiredSupplied(t *testing.T) {
	prompts := map[string]cuetry.RecipePrompt{
		"host": {Required: true},
	}
	out, err := cuetry.ValidateAndApplyPromptDefaults(prompts, map[string]string{"host": "10.0.0.1"})
	require.NoError(t, err)
	require.Equal(t, "10.0.0.1", out["host"])
}

func TestValidateAndApplyPromptDefaults_SuppliedOverridesDefault(t *testing.T) {
	prompts := map[string]cuetry.RecipePrompt{
		"env": {Required: true, Default: "staging"},
	}
	out, err := cuetry.ValidateAndApplyPromptDefaults(prompts, map[string]string{"env": "prod"})
	require.NoError(t, err)
	require.Equal(t, "prod", out["env"])
}

func TestValidateAndApplyPromptDefaults_OptionalMissing(t *testing.T) {
	prompts := map[string]cuetry.RecipePrompt{
		"tag": {Required: false},
	}
	out, err := cuetry.ValidateAndApplyPromptDefaults(prompts, nil)
	require.NoError(t, err)
	_, ok := out["tag"]
	require.False(t, ok)
}

func TestValidateAndApplyPromptDefaults_DoesNotMutateInput(t *testing.T) {
	prompts := map[string]cuetry.RecipePrompt{
		"env": {Default: "staging"},
	}
	orig := map[string]string{"other": "x"}
	out, err := cuetry.ValidateAndApplyPromptDefaults(prompts, orig)
	require.NoError(t, err)
	_, mutated := orig["env"]
	require.False(t, mutated)
	require.Equal(t, "staging", out["env"])
	require.Equal(t, "x", out["other"])
}
