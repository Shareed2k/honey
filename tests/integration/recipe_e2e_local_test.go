package integration

import (
	"context"
	"strings"
	"testing"
	"time"
	"os"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecipeE2E_LocalCommandWorkspace(t *testing.T) {
	// Create a MatchLocalAIHost record
	rec := hosts.Record{Provider: "test", Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"}

	cueContent := `
recipe: {
	name: "test-local-workspace"
	steps: [
		{
			host: "_"
			command: "echo 'hello workspace' > ${HONEY_WORKSPACE}/data.txt"
		},
		{
			host: "_"
			command: "cat ${HONEY_WORKSPACE}/data.txt"
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cliEnv := make(map[string]string)
	_, cleanup, err := engine.SetupRecipeWorkspace(cliEnv)
	require.NoError(t, err)
	defer cleanup()

	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		Execute: true,
		CLIEnv:  cliEnv,
	}

	outCh := make(chan engine.HostExecResult, 10)
	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 2)
	assert.True(t, results[0].Success, "Step 1 failed: %s\n%s", results[0].ErrMsg, results[0].Output)
	assert.True(t, results[1].Success, "Step 2 failed: %s\n%s", results[1].ErrMsg, results[1].Output)
	assert.Equal(t, "hello workspace", strings.TrimSpace(results[1].Output))
}

func TestRecipeE2E_LocalScript(t *testing.T) {
	rec := hosts.Record{Provider: "test", Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"}

	// For the legacy script string parser
	tmpScript := "/tmp/honey-e2e-local-script.sh"
	os.WriteFile(tmpScript, []byte("echo 'local script ran'"), 0600)
	defer os.Remove(tmpScript)

	cueContent := `
recipe: {
	name: "test-local-script"
	steps: [
		{
			host: "_"
			script: {
				local: "/tmp/honey-e2e-local-script.sh"
				remote: """
					echo 'script execution'
					pwd
				"""
			}
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		RecipeDir: "/tmp",
		Records: []hosts.Record{rec},
		Execute: true,
	}

	outCh := make(chan engine.HostExecResult, 10)
	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 1)
	assert.True(t, results[0].Success, "Step 1 failed: %s\n%s", results[0].ErrMsg, results[0].Output)
	// Because script is a local file, local script overrides inline string for local/remote struct format.
	assert.Contains(t, results[0].Output, "local script ran")
}
