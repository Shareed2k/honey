package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/commandrisk"
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
	denyAll := mustPolicy(t, `package honey
import rego.v1
default allow := false
default deny_reason := "admission denied in test"
`)
	r := NewRecipeRunner(RunnerOptions{Enforcer: denyAll})

	ch, err := r.Execute(context.Background(), RunRequest{
		Recipe:           parseTestRecipe(t, dryRunRecipe),
		RecipeSourcePath: "deploy.cue",
		Records:          []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}},
		ActorID:          "alice",
	})
	require.Error(t, err)
	require.Nil(t, ch)
	require.Contains(t, err.Error(), "admission denied in test")
}

func TestRecipeRunner_Admission_AllowProceeds(t *testing.T) {
	allowAll := mustPolicy(t, `package honey
import rego.v1
default allow := true
`)
	r := NewRecipeRunner(RunnerOptions{Enforcer: allowAll})

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

// --- Dry-run risk assessment (v2 Task 6) ----------------------------------

func TestAssessCommandRisk(t *testing.T) {
	const recipe = `
recipe: {
	name: "risk-assess"
	type: "linear"
	steps: [
		{ host: "*", command: "rm -rf /" },
		{ host: "*", command: "uptime" },
	]
}
`
	r := NewRecipeRunner(RunnerOptions{})
	got := r.AssessCommandRisk(context.Background(), RunRequest{Recipe: parseTestRecipe(t, recipe)})
	require.Len(t, got, 2)

	require.Equal(t, "rm -rf /", got[0].Command)
	require.True(t, got[0].Analysis.Critical)
	require.Equal(t, commandrisk.SeverityCritical, got[0].Analysis.MaxSeverity)

	require.Equal(t, "uptime", got[1].Command)
	require.False(t, got[1].Analysis.Critical)
	require.Empty(t, got[1].Analysis.Signals)
}

// --- Biometric admission gate (v2 Task 8) ---------------------------------

type stubBiometric struct{ validToken string }

func (s stubBiometric) VerifyToken(_, token string) bool { return token == s.validToken }

func TestAdmitRecipe_RequireBiometric(t *testing.T) {
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := false
default deny_reason := ""
default decision := ""
allow if { input.action == "recipe_execute"; input.execution.biometricVerified == true }
decision := "require_biometric" if { input.action == "recipe_execute"; not input.execution.biometricVerified }
deny_reason := "biometric required" if { input.action == "recipe_execute"; not input.execution.biometricVerified }`)

	r := NewRecipeRunner(RunnerOptions{
		Enforcer:  enf,
		Biometric: stubBiometric{validToken: "good-token"},
	})

	// No token → denied.
	err := r.admitRecipe(context.Background(), RunRequest{ActorID: "alice", RecipeSourcePath: "r.cue"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "biometric")

	// Valid token → re-eval with biometricVerified=true → admitted.
	err = r.admitRecipe(context.Background(), RunRequest{ActorID: "alice", RecipeSourcePath: "r.cue", BiometricToken: "good-token"})
	require.NoError(t, err)

	// Wrong token → denied.
	err = r.admitRecipe(context.Background(), RunRequest{ActorID: "alice", RecipeSourcePath: "r.cue", BiometricToken: "bad"})
	require.Error(t, err)
}

// --- Command risk gate ----------------------------------------------------

func TestGateCommandRisk_BuiltinCritical(t *testing.T) {
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "alice"}} // no enforcer
	targets := []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}}

	// Critical built-in pattern is denied even without an OPA enforcer.
	allowed, skipped, err := gateCommandRisk(context.Background(), run, "command", "rm -rf /", "", targets)
	require.NoError(t, err)
	require.Empty(t, allowed)
	require.Len(t, skipped, 1)
	require.True(t, skipped[0].Skipped)
	require.Contains(t, skipped[0].Output, "command risk")

	// Safe command passes through untouched.
	allowed, skipped, err = gateCommandRisk(context.Background(), run, "command", "uptime", "", targets)
	require.NoError(t, err)
	require.Len(t, allowed, 1)
	require.Empty(t, skipped)
}

func TestGateCommandRisk_OPAContextual(t *testing.T) {
	// Deny high-severity commands on prod hosts; allow elsewhere.
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.env == "prod"
}
deny_reason := "high-risk command on prod" if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.env == "prod"
}`)
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "alice", Enforcer: enf}}
	targets := []hosts.Record{
		{Provider: "static", Name: "prod1", PrimaryIP: "1.1.1.1", Meta: map[string]string{"env": "prod"}},
		{Provider: "static", Name: "stg1", PrimaryIP: "2.2.2.2", Meta: map[string]string{"env": "staging"}},
	}

	allowed, skipped, err := gateCommandRisk(context.Background(), run, "command", "systemctl stop nginx", "", targets)
	require.NoError(t, err)
	require.Len(t, allowed, 1)
	require.Equal(t, "stg1", allowed[0].Name)
	require.Len(t, skipped, 1)
	require.Equal(t, "prod1", skipped[0].Name)
	require.Contains(t, skipped[0].Output, "high-risk command on prod")
}

func TestGateCommandRisk_Disabled(t *testing.T) {
	t.Setenv("HONEY_RISK_DISABLE", "1")
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "alice"}}
	targets := []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}}
	allowed, skipped, err := gateCommandRisk(context.Background(), run, "command", "rm -rf /", "", targets)
	require.NoError(t, err)
	require.Len(t, allowed, 1, "disable env bypasses even critical deny")
	require.Empty(t, skipped)
}

func TestGateCommandRisk_PythonInterpreter(t *testing.T) {
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "alice"}} // no enforcer
	targets := []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}}

	// A python step shelling out to a critical command is denied via the
	// gpython analyzer recursing into the shell detectors.
	allowed, skipped, err := gateCommandRisk(context.Background(), run, "command", `os.system("rm -rf /")`, "python3", targets)
	require.NoError(t, err)
	require.Empty(t, allowed)
	require.Len(t, skipped, 1)
	require.Contains(t, skipped[0].Output, "command risk")

	// Benign python is not shell-parsed (no bogus UNPARSEABLE_COMMAND) → allowed.
	allowed, skipped, err = gateCommandRisk(context.Background(), run, "command", "print(\"hi\")", "python3", targets)
	require.NoError(t, err)
	require.Len(t, allowed, 1)
	require.Empty(t, skipped)
}

func TestGateCommandRisk_InterpreterInPolicyInput(t *testing.T) {
	// Policy keys on input.command.interpreter to deny any python step.
	enf := mustPolicy(t, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "command_exec"
	input.command.interpreter == "python3"
}
deny_reason := "python steps not allowed here" if {
	input.action == "command_exec"
	input.command.interpreter == "python3"
}`)
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "alice", Enforcer: enf}}
	targets := []hosts.Record{{Provider: "static", Name: "h1", PrimaryIP: "1.1.1.1"}}

	allowed, skipped, err := gateCommandRisk(context.Background(), run, "command", "print(\"hi\")", "python3", targets)
	require.NoError(t, err)
	require.Empty(t, allowed)
	require.Len(t, skipped, 1)
	require.Contains(t, skipped[0].Output, "python steps not allowed here")
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
