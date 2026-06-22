package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// TestCueRun_StepEnv_mergesDefaultsAndCLIEnv verifies StepEnv pulls all the
// run-scoped inputs from the receiver and merges recipe defaults with CLI env.
func TestCueRun_StepEnv_mergesDefaultsAndCLIEnv(t *testing.T) {
	recipe := parseTestRecipe(t, `
recipe: {
	name: "env-test"
	type: "linear"
	defaults: { env: { FROM_DEFAULTS: "d" } }
	steps: [ { host: "*", command: "echo hi" } ]
}
`)
	run := &CueRun{Params: CueRecipeRunParams{
		Recipe: recipe,
		CLIEnv: map[string]string{"FROM_CLI": "c"},
	}}

	step := recipe.Steps[0].Step.Base()
	target := hosts.Record{Provider: "test", Name: "h1", PrimaryIP: "10.0.0.1"}

	env, err := run.StepEnv(context.Background(), step, &target, false, true)
	require.NoError(t, err)
	require.Equal(t, "d", env["FROM_DEFAULTS"])
	require.Equal(t, "c", env["FROM_CLI"])
}

// compile-time guard: signature matches what step executors call.
var _ = func(run *CueRun, ctx context.Context, b *cuetry.StepBase, r *hosts.Record) (map[string]string, error) {
	return run.StepEnv(ctx, b, r, true, false)
}
