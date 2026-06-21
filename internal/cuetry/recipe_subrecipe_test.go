package cuetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRemoteRecipe_recipeStep(t *testing.T) {
	cueBytes := []byte(`
recipe: {
	name: "test subrecipe"
	steps: [
		{
			host: "*"
			recipe: {
				path: "my-subrecipe.cue"
				prompts: {
					"env": "prod"
				}
			}
		}
	]
}
`)
	r, err := ParseRemoteRecipe(cueBytes, nil)
	assert.NoError(t, err)
	assert.Len(t, r.Steps, 1)

	step, ok := r.Steps[0].Step.(*RecipeStep)
	assert.True(t, ok, "step should be RecipeStep")
	assert.Equal(t, KindRecipe, step.Kind())
	assert.Equal(t, "my-subrecipe.cue", step.Recipe.Path)
	assert.Equal(t, map[string]string{"env": "prod"}, step.Recipe.Prompts)
}

func TestParseRemoteRecipe_recipeMissingPath(t *testing.T) {
	cueBytes := []byte(`
recipe: {
	name: "test subrecipe empty"
	steps: [
		{
			host: "*"
			recipe: {
				prompts: {
					"env": "prod"
				}
			}
		}
	]
}
`)
	_, err := ParseRemoteRecipe(cueBytes, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cuetry: validate:")
}
