package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
)

// fakePluginProvider satisfies PluginProvider without opening real plugins.
type fakePluginProvider struct {
	released int
}

func (f *fakePluginProvider) Borrow() (*plugins.Manager, func()) {
	return nil, func() { f.released++ }
}

// parseTestRecipe is a small helper to parse inline CUE for tests.
func parseTestRecipe(t *testing.T, content string) cuetry.Recipe {
	t.Helper()
	rec, err := cuetry.ParseRemoteRecipe([]byte(content), nil)
	require.NoError(t, err)
	return rec
}

const dryRunRecipe = `
recipe: {
	name: "dry-test"
	type: "linear"
	steps: [
		{ host: "*", command: "echo hello" },
	]
}
`

func TestRecipeRunner_DryRun_returnsPlanAndReleasesPlugin(t *testing.T) {
	fp := &fakePluginProvider{}
	r := NewRecipeRunner(RunnerOptions{Plugins: fp})

	plan, err := r.DryRun(context.Background(), RunRequest{
		Recipe:  parseTestRecipe(t, dryRunRecipe),
		Records: []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}},
		Env:     nil,
	})
	require.NoError(t, err)
	require.Contains(t, plan, "echo hello")
	// Executor-based dry-run emits the trailing notice; static RenderDryRunPlan
	// does not. Asserting it locks DryRun to the executor path (matches the old
	// handleCueExec behavior consumed by clients).
	require.Contains(t, plan, "Dry-run only")
	require.Equal(t, 1, fp.released, "borrowed plugin manager must be released")
}

func TestRecipeRunner_DryRun_missingRequiredPromptErrors(t *testing.T) {
	fp := &fakePluginProvider{}
	r := NewRecipeRunner(RunnerOptions{Plugins: fp})

	const recipeWithPrompt = `
recipe: {
	name: "needs-prompt"
	type: "linear"
	defaults: prompts: { TARGET: { required: true } }
	steps: [ { host: "*", command: "echo $TARGET" } ]
}
`
	_, err := r.DryRun(context.Background(), RunRequest{
		Recipe: parseTestRecipe(t, recipeWithPrompt),
		Env:    nil,
	})
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "TARGET"), "error should name the missing prompt")
	require.Equal(t, 1, fp.released, "plugin must be released even on validation error")
}

func TestRecipeRunner_Execute_missingRequiredPromptErrorsSync(t *testing.T) {
	fp := &fakePluginProvider{}
	r := NewRecipeRunner(RunnerOptions{Plugins: fp})

	const recipeWithPrompt = `
recipe: {
	name: "needs-prompt"
	type: "linear"
	defaults: prompts: { TARGET: { required: true } }
	steps: [ { host: "*", command: "echo $TARGET" } ]
}
`
	ch, err := r.Execute(context.Background(), RunRequest{
		Recipe: parseTestRecipe(t, recipeWithPrompt),
		Env:    nil,
	})
	require.Error(t, err, "missing required prompt is a synchronous pre-flight error")
	require.Nil(t, ch)
	require.Equal(t, 1, fp.released, "plugin must be released on pre-flight error")
}

func TestRecipeRunner_Execute_releasesPluginAfterChannelDrains(t *testing.T) {
	fp := &fakePluginProvider{}
	// No ExecRegistry/records → StreamCueRecipeSteps returns the "no hosts" error,
	// which the runner surfaces as a synthetic failed result, then closes the channel.
	r := NewRecipeRunner(RunnerOptions{Plugins: fp})

	ch, err := r.Execute(context.Background(), RunRequest{
		Recipe:  parseTestRecipe(t, dryRunRecipe),
		Records: nil,
	})
	require.NoError(t, err, "pre-flight (prompt+secret) succeeded; run errors arrive on the channel")
	require.NotNil(t, ch)

	var got []HostExecResult
	for res := range ch {
		got = append(got, res)
	}
	// Channel is closed → plugin released exactly once.
	require.Equal(t, 1, fp.released)
	require.NotEmpty(t, got, "a run with no hosts emits a synthetic failed result")
	require.False(t, got[len(got)-1].Success)
}

func TestRecipeRunner_ExecuteAndWait_surfacesRunError(t *testing.T) {
	fp := &fakePluginProvider{}
	r := NewRecipeRunner(RunnerOptions{Plugins: fp})

	// No records → run fails; ExecuteAndWait drains and returns the run error.
	err := r.ExecuteAndWait(context.Background(), RunRequest{
		Recipe:  parseTestRecipe(t, dryRunRecipe),
		Records: nil,
	})
	require.Error(t, err)
	require.Equal(t, 1, fp.released, "plugin released after the run drains")
}

func TestRecipeRunner_ExecuteAndWait_preflightError(t *testing.T) {
	fp := &fakePluginProvider{}
	r := NewRecipeRunner(RunnerOptions{Plugins: fp})

	const recipeWithPrompt = `
recipe: {
	name: "needs-prompt"
	type: "linear"
	defaults: prompts: { TARGET: { required: true } }
	steps: [ { host: "*", command: "echo $TARGET" } ]
}
`
	err := r.ExecuteAndWait(context.Background(), RunRequest{
		Recipe: parseTestRecipe(t, recipeWithPrompt),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "TARGET")
	require.Equal(t, 1, fp.released)
}
