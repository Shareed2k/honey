package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

func TestRecipeExecutor_ExecuteStream(t *testing.T) {
	// Create a temp directory for our recipe and sub-recipe
	tmpDir := t.TempDir()

	// Create the sub-recipe file
	subRecipeCUE := `
recipe: {
	name: "sub-recipe"
	steps: [
		{
			host: "_"
			template: {
				template: "hello from sub-recipe"
			}
		}
	]
}
`
	subRecipePath := filepath.Join(tmpDir, "sub.cue")
	err := os.WriteFile(subRecipePath, []byte(subRecipeCUE), 0o644)
	require.NoError(t, err)

	// Create the main recipe file
	mainRecipeCUE := `
recipe: {
	name: "main-recipe"
	steps: [
		{
			host: "_"
			recipe: {
				path: "sub.cue"
				prompts: {
					test_message: "test-value"
				}
			}
		}
	]
}
`
	mainRecipePath := filepath.Join(tmpDir, "main.cue")
	err = os.WriteFile(mainRecipePath, []byte(mainRecipeCUE), 0o644)
	require.NoError(t, err)

	records := []hosts.Record{
		{Name: "localhost", Provider: "local"},
	}

	mainBytes, err := os.ReadFile(mainRecipePath)
	require.NoError(t, err)

	mainRecipe, err := cuetry.ParseRemoteRecipe(mainBytes, records)
	require.NoError(t, err)

	outCh := make(chan engine.HostExecResult, 10)

	params := engine.CueRecipeRunParams{
		Recipe:    mainRecipe,
		RecipeDir: tmpDir,
		Records:   records,
		Execute:   true,
	}

	err = engine.StreamCueRecipeSteps(context.Background(), params, outCh)
	require.NoError(t, err)
	close(outCh)

	var results []engine.HostExecResult
	for r := range outCh {
		results = append(results, r)
	}

	require.Len(t, results, 1)
	require.True(t, results[0].Success)
	require.Contains(t, results[0].Output, "hello from sub-recipe")
}
