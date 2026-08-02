package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

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

func TestRecipeRunner_DryRun_returnsPlan(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{})

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
}

func TestRecipeRunner_DryRun_recordSessionCreatesRecording(t *testing.T) {
	dir := t.TempDir()
	r := NewRecipeRunner(RunnerOptions{RecordDir: dir})

	plan, err := r.DryRun(context.Background(), RunRequest{
		Recipe:           parseTestRecipe(t, dryRunRecipe),
		RecipeSourcePath: "inline.cue",
		Records:          []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}},
		RecordSession:    true,
		RecordLabel:      "web-cue-exec-dry",
	})
	require.NoError(t, err)
	require.Contains(t, plan, "echo hello")

	raw := readOnlyRecording(t, dir)
	require.Equal(t, 1, strings.Count(raw, `"type":"recipe-meta"`), raw)
	require.Contains(t, raw, `"recipe_path":"inline.cue"`)
	require.Contains(t, raw, `"host_count":1`)
	require.Contains(t, raw, `"direction":"plan"`)
	require.Contains(t, raw, `"type":"close"`)
}

func TestRecipeRunner_DryRun_missingRequiredPromptErrors(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{})

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
}

func TestRecipeRunner_Execute_missingRequiredPromptErrorsSync(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{})

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
}

func TestRecipeRunner_Execute_handlesRunError(t *testing.T) {
	// No ExecRegistry/records → StreamCueRecipeSteps returns the "no hosts" error,
	// which the runner surfaces as a synthetic failed result, then closes the channel.
	r := NewRecipeRunner(RunnerOptions{})

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
	require.NotEmpty(t, got, "a run with no hosts emits a synthetic failed result")
	require.False(t, got[len(got)-1].Success)
}

func TestRecipeRunner_Execute_recordSessionCreatesRecording(t *testing.T) {
	dir := t.TempDir()
	r := NewRecipeRunner(RunnerOptions{RecordDir: dir})

	ch, err := r.Execute(context.Background(), RunRequest{
		Recipe:           parseTestRecipe(t, dryRunRecipe),
		RecipeSourcePath: "run.cue",
		RecordSession:    true,
		RecordLabel:      "web-cue-exec",
	})
	require.NoError(t, err)

	//nolint:revive // empty block is required to drain the channel
	for range ch {
	}

	raw := readOnlyRecording(t, dir)
	require.Equal(t, 1, strings.Count(raw, `"type":"recipe-meta"`), raw)
	require.Contains(t, raw, `"recipe_path":"run.cue"`)
	require.Contains(t, raw, `"host_count":0`)
	require.Contains(t, raw, `"type":"error"`)
	require.Contains(t, raw, `"no hosts in current result set"`)
	require.Contains(t, raw, `"type":"close"`)
}

func TestRecipeRunner_Execute_injectedRecorderRecordsSingleRecipeMeta(t *testing.T) {
	dir := t.TempDir()
	rec, err := NewBatchSessionRecorder(dir, "web-cue-exec", "alice", 0)
	require.NoError(t, err)

	r := NewRecipeRunner(RunnerOptions{})
	ch, err := r.Execute(context.Background(), RunRequest{
		Recipe:           parseTestRecipe(t, dryRunRecipe),
		RecipeSourcePath: "stream.cue",
		Recorder:         rec,
	})
	require.NoError(t, err)

	//nolint:revive // empty block is required to drain the channel
	for range ch {
	}
	require.NoError(t, rec.Close())

	raw, err := os.ReadFile(rec.Path())
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(raw), `"type":"recipe-meta"`), string(raw))
	require.Contains(t, string(raw), `"recipe_path":"stream.cue"`)
}

func TestRecipeRunner_ExecuteAndWait_surfacesRunError(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{})

	// No records → run fails; ExecuteAndWait drains and returns the run error.
	err := r.ExecuteAndWait(context.Background(), RunRequest{
		Recipe:  parseTestRecipe(t, dryRunRecipe),
		Records: nil,
	})
	require.Error(t, err)
}

func TestRecipeRunner_ExecuteAndWait_preflightError(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{})

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
}

func TestRecipeRunner_Execute_GraphTriggerRule(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{})

	const triggerRecipe = `
recipe: {
	name: "trigger-test"
	type: "graph"
	steps: [
		{ id: "fail_step", host: "_", command: "exit 1", ignore_errors: false },
		{ id: "skip_step", host: "_", depends: ["fail_step"], command: "echo skip" },
		{ id: "rescue_step", host: "_", depends: ["fail_step"], trigger_rule: "one_failed", command: "echo rescued" },
	]
}
`
	// execute should fail because one of the steps failed and ignore_errors is false.
	// But it should still return the channel so we can observe "rescue_step" running.
	ch, _ := r.Execute(context.Background(), RunRequest{
		Recipe:  parseTestRecipe(t, triggerRecipe),
		Records: []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "127.0.0.1"}},
	})
	// Actually Execute returns (<-chan, error) where error is preflight. It will return nil err here.

	var got []HostExecResult
	for res := range ch {
		got = append(got, res)
	}

	hasRescue := false
	hasSkip := false
	for _, res := range got {
		if strings.Contains(res.Name, "Step 3") || strings.Contains(res.Name, "rescue_step") {
			hasRescue = true
		}
		if strings.Contains(res.Name, "Step 2") || strings.Contains(res.Name, "skip_step") {
			hasSkip = true
		}
	}
	require.True(t, hasRescue, "rescue_step should have attempted to execute")
	require.False(t, hasSkip, "skip_step should NOT have executed")
}

func TestRecipeRunner_Execute_GraphRescueBlock(t *testing.T) {
	r := NewRecipeRunner(RunnerOptions{})

	const rescueRecipe = `
recipe: {
	name: "rescue-test"
	type: "graph"
	steps: [
		{ id: "fail_step", host: "_", command: "exit 1", ignore_errors: false, rescue: ["rescue_step"] },
		{ id: "skip_step", host: "_", depends: ["fail_step"], command: "echo skip" },
		{ id: "rescue_step", host: "_", command: "echo rescued" },
	]
}
`
	ch, _ := r.Execute(context.Background(), RunRequest{
		Recipe:  parseTestRecipe(t, rescueRecipe),
		Records: []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "127.0.0.1"}},
	})

	var got []HostExecResult
	for res := range ch {
		got = append(got, res)
	}

	hasRescue := false
	hasSkip := false
	for _, res := range got {
		if strings.Contains(res.Name, "Step 3") || strings.Contains(res.Name, "rescue_step") {
			hasRescue = true
		}
		if strings.Contains(res.Name, "Step 2") || strings.Contains(res.Name, "skip_step") {
			hasSkip = true
		}
	}
	require.True(t, hasRescue, "rescue_step should have attempted to execute")
	require.False(t, hasSkip, "skip_step should NOT have executed")
}

func TestRecipeRunner_Execute_GraphMapReduce(t *testing.T) {
	old, _ := GetStepExecutor(cuetry.KindCommand)
	RegisterStepExecutor(cuetry.KindCommand, &mockReduceExecutor{})
	defer func() { RegisterStepExecutor(cuetry.KindCommand, old) }()

	r := NewRecipeRunner(RunnerOptions{})

	const reduceRecipe = `
recipe: {
	name: "reduce-test"
	type: "graph"
	steps: [
		{ id: "gen", host: "_", loop: "[\"a\", \"b\"]", command: "echo {{.item}}", reduce: "reduced_data" },
		{ id: "agg", host: "_", depends: ["gen"], env_from: [{from_output: "reduced_data", map: {"DATA": "stdout"}}], command: "echo $DATA" },
	]
}
`
	ch, _ := r.Execute(context.Background(), RunRequest{
		Recipe:  parseTestRecipe(t, reduceRecipe),
		Records: []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "127.0.0.1"}},
	})

	var got []HostExecResult
	for res := range ch {
		got = append(got, res)
	}

	hasAgg := false
	for _, res := range got {
		t.Logf("Got result: ID=%s, Success=%v, Skipped=%v, Err=%q, Output=%q", res.Name, res.Success, res.Skipped, res.ErrMsg, res.Output)
		if strings.Contains(res.Name, "Step 2") || strings.Contains(res.Name, "agg") {
			// output of agg should be the JSON array ["a","b"]
			if strings.Contains(res.Output, "[\"a\",\"b\"]") || strings.Contains(res.Output, "[\"b\",\"a\"]") {
				hasAgg = true
			}
		}
	}
	require.True(t, hasAgg, "reduce step should have aggregated the array and passed it via env_from")
}

func readOnlyRecording(t *testing.T, dir string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	var path string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".hrec.jsonl") {
			require.Empty(t, path, "expected one recording file")
			path = filepath.Join(dir, entry.Name())
		}
	}
	require.NotEmpty(t, path, "expected a recording file")

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(raw)
}
