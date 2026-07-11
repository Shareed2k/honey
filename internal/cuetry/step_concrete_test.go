package cuetry

import (
	"strings"
	"testing"
)

// TestStepValidate_RunAsUnsupportedKinds locks in Candidate #7 from the
// architecture review: "which kinds support run_as" now lives on each kind's
// own Validate() (previously a single switch in recipe.go's validateStepRunAs).
// The check must fire before any other field validation on the step, matching
// the old centralized-gate-runs-before-Validate() ordering.
func TestStepValidate_RunAsUnsupportedKinds(t *testing.T) {
	tests := []struct {
		name string
		step Step
	}{
		{"put", &PutStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"get", &GetStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"agent_transfer", &AgentTransferStep{StepBase: StepBase{RunAs: "deploy", Host: "a"}}},
		{"ai", &AIStep{StepBase: StepBase{RunAs: "deploy", Host: MatchLocalAIHost}}},
		{"template", &TemplateStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"tunnel", &TunnelStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"k8s", &K8sStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"opensearch", &OpensearchStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"postgres", &PostgresStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"recipe", &RecipeStep{StepBase: StepBase{RunAs: "deploy"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.step.Validate(StepValidateCtx{Index: 1, NumSteps: 2})
			if err == nil {
				t.Fatalf("%s: expected run_as rejection, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), "run_as") {
				t.Fatalf("%s: expected a run_as-related error, got: %v", tt.name, err)
			}
		})
	}
}

// TestStepValidate_RunAsSupportedKinds guards against accidentally rejecting
// run_as on a kind that's supposed to allow it.
func TestStepValidate_RunAsSupportedKinds(t *testing.T) {
	tests := []struct {
		name string
		step Step
	}{
		{"command", &CommandStep{StepBase: StepBase{RunAs: "deploy"}}},
		{"plugin", &PluginStep{StepBase: StepBase{RunAs: "deploy"}, Plugin: &RecipeStepPlugin{ID: "x", Action: "y"}}},
		{"docker", &DockerStep{StepBase: StepBase{RunAs: "deploy"}, Docker: &RecipeStepDocker{Action: "ps"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.step.Validate(StepValidateCtx{Index: 1, NumSteps: 2})
			if err != nil && strings.Contains(err.Error(), "run_as") {
				t.Fatalf("%s: run_as should be allowed on this kind, got: %v", tt.name, err)
			}
		})
	}
}

// TestStepValidate_EnvUnsupportedKinds locks in the other Candidate #7
// migration: env is rejected for agent_transfer/ai in their own Validate(),
// no longer via a centralized kind-switch in recipe.go.
func TestStepValidate_EnvUnsupportedKinds(t *testing.T) {
	tests := []struct {
		name string
		step Step
	}{
		{"agent_transfer", &AgentTransferStep{StepBase: StepBase{Env: map[string]string{"FOO": "bar"}, Host: "a"}}},
		{"ai", &AIStep{StepBase: StepBase{Env: map[string]string{"FOO": "bar"}, Host: MatchLocalAIHost}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.step.Validate(StepValidateCtx{Index: 1, NumSteps: 2})
			if err == nil {
				t.Fatalf("%s: expected env rejection, got nil", tt.name)
			}
			if !strings.Contains(err.Error(), "env") {
				t.Fatalf("%s: expected an env-related error, got: %v", tt.name, err)
			}
		})
	}
}

// TestPluginStepValidate_KVKey covers plugin.kv_key format checking, added
// alongside the plugin/postgres kv_key precedent.
func TestPluginStepValidate_KVKey(t *testing.T) {
	tests := []struct {
		name    string
		kvKey   string
		wantErr bool
	}{
		{"empty is fine (kv_key optional)", "", false},
		{"plain key ok", "stealth_fetch", false},
		{"contains slash rejected", "bad/key", true},
		{"too long rejected", strings.Repeat("k", 300), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &PluginStep{
				StepBase: StepBase{Host: "*"},
				Plugin:   &RecipeStepPlugin{ID: "duckdb", Action: "query", KVKey: tt.kvKey},
			}
			err := s.Validate(StepValidateCtx{Index: 0, NumSteps: 1})
			if tt.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

// TestCommandStepValidate_Templated covers the syntax-only template parse
// check run when templated: true — catches malformed {{ }} at cue-validate
// time rather than only at execute time.
func TestCommandStepValidate_Templated(t *testing.T) {
	tests := []struct {
		name      string
		command   string
		templated bool
		wantErr   bool
	}{
		{"not templated, malformed braces ignored", "echo {{ not a template", false, false},
		{"templated, valid syntax", `echo {{ kvGet "key" }}`, true, false},
		{"templated, unclosed action", "echo {{ .Bad ", true, true},
		{"templated, unknown function", `echo {{ notAFunc "x" }}`, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &CommandStep{
				StepBase:  StepBase{Host: "*"},
				Command:   tt.command,
				Templated: tt.templated,
			}
			err := s.Validate(StepValidateCtx{Index: 0, NumSteps: 1})
			if tt.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}
