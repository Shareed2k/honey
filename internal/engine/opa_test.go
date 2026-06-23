package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
)

func mustPolicy(t *testing.T, src string) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "test.rego", src)
	require.NoError(t, err)
	return enf
}

// --- Scenario 1: recipe admission control --------------------------------

func TestRecipeRunner_Admission_DenyBlocksExecute(t *testing.T) {
	fp := &fakePluginProvider{}
	denyAll := mustPolicy(t, `package honey
import rego.v1
default allow := false
default deny_reason := "admission denied in test"
`)
	r := NewRecipeRunner(RunnerOptions{Plugins: fp, Enforcer: denyAll})

	ch, err := r.Execute(context.Background(), RunRequest{
		Recipe:           parseTestRecipe(t, dryRunRecipe),
		RecipeSourcePath: "deploy.cue",
		Records:          []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}},
		ActorID:          "alice",
	})
	require.Error(t, err)
	require.Nil(t, ch)
	require.Contains(t, err.Error(), "admission denied in test")
	require.Equal(t, 0, fp.released, "admission denial must short-circuit before borrowing a plugin")
}

func TestRecipeRunner_Admission_AllowProceeds(t *testing.T) {
	fp := &fakePluginProvider{}
	allowAll := mustPolicy(t, `package honey
import rego.v1
default allow := true
`)
	r := NewRecipeRunner(RunnerOptions{Plugins: fp, Enforcer: allowAll})

	// nil records → run-time "no hosts" surfaces on the channel, but admission
	// passed (no pre-flight error), proving the gate let it through.
	ch, err := r.Execute(context.Background(), RunRequest{
		Recipe:           parseTestRecipe(t, dryRunRecipe),
		RecipeSourcePath: "deploy.cue",
		ActorID:          "alice",
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	for range ch { //nolint:revive // drain
	}
	require.Equal(t, 1, fp.released)
}

// --- Scenario 4: host-list filtering -------------------------------------

func TestFilterTargetsByPolicy(t *testing.T) {
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := false
default deny_reason := "host blocked"
allow if input.host != "h2"
`)
	run := &CueRun{Params: CueRecipeRunParams{Enforcer: enf, ActorID: "alice"}}
	targets := []hosts.Record{
		{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"},
		{Provider: "static", Name: "h2", PrimaryIP: "2.2.2.2"},
		{Provider: "static", Name: "h3", PrimaryIP: "3.3.3.3"},
	}

	kept, skipped, err := filterTargetsByPolicy(context.Background(), run, "command", targets)
	require.NoError(t, err)
	require.Len(t, kept, 2)
	require.Equal(t, "h1", kept[0].Name)
	require.Equal(t, "h3", kept[1].Name)
	require.Len(t, skipped, 1)
	require.Equal(t, "h2", skipped[0].Name)
	require.True(t, skipped[0].Skipped)
	require.Contains(t, skipped[0].Output, "host blocked")
}

func TestFilterTargetsByPolicy_HostVars(t *testing.T) {
	// Policy denies a host whose resolved inventory var tier == "prod".
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "step_execute"
	input.host_vars.tier == "prod"
}
deny_reason := "prod host blocked" if {
	input.action == "step_execute"
	input.host_vars.tier == "prod"
}`)
	inv := config.Inventory{Hosts: map[string]config.InventoryHost{
		"h2": {Vars: map[string]config.InventoryValue{"tier": config.MustInventoryValue("prod")}},
	}}
	run := &CueRun{Params: CueRecipeRunParams{Enforcer: enf, ActorID: "alice", Inventory: inv}}
	targets := []hosts.Record{
		{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"},
		{Provider: "static", Name: "h2", PrimaryIP: "2.2.2.2"},
	}

	kept, skipped, err := filterTargetsByPolicy(context.Background(), run, "command", targets)
	require.NoError(t, err)
	require.Len(t, kept, 1)
	require.Equal(t, "h1", kept[0].Name)
	require.Len(t, skipped, 1)
	require.Equal(t, "h2", skipped[0].Name)
	require.Contains(t, skipped[0].Output, "prod host blocked")
}

func TestFilterTargetsByPolicy_NilEnforcerPassThrough(t *testing.T) {
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "alice"}}
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}}
	kept, skipped, err := filterTargetsByPolicy(context.Background(), run, "command", targets)
	require.NoError(t, err)
	require.Equal(t, targets, kept)
	require.Nil(t, skipped)
}

// --- Scenario 2: opa step type (in-recipe) -------------------------------

const opaStepRecipe = `
recipe: {
	name: "opa-step-test"
	type: "linear"
	steps: [
		{ host: "_", opa: { policy: "policy.rego" } },
	]
}
`

func runOPAStepRecipe(t *testing.T, policyRego string) ([]HostExecResult, error) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(policyRego), 0o600))

	params := CueRecipeRunParams{
		Recipe:    parseTestRecipe(t, opaStepRecipe),
		RecipeDir: dir,
		Records:   []hosts.Record{{Provider: "local", Name: "local"}},
		Execute:   true,
	}
	out := make(chan HostExecResult, 8)
	err := StreamCueRecipeSteps(context.Background(), params, out)
	close(out)

	var results []HostExecResult
	for r := range out {
		results = append(results, r)
	}
	return results, err
}

func TestOPAStep_Allow(t *testing.T) {
	results, err := runOPAStepRecipe(t, `package honey
import rego.v1
default allow := true
`)
	require.NoError(t, err)
	require.NotEmpty(t, results)
	last := results[len(results)-1]
	require.True(t, last.Success, "allow policy should pass the step: %+v", last)
}

func TestOPAStep_Deny(t *testing.T) {
	results, err := runOPAStepRecipe(t, `package honey
import rego.v1
default allow := false
default deny_reason := "step blocked by policy"
`)
	require.Error(t, err, "deny should fail the recipe")
	require.True(t, strings.Contains(err.Error(), "step blocked by policy"),
		"error should carry deny_reason, got: %v", err)
	require.NotEmpty(t, results)
	require.False(t, results[len(results)-1].Success)
}
