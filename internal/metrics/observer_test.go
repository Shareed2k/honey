package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestObserverMethodsExposeMetrics(t *testing.T) {
	reg := NewRegistry("test", "abc123")
	obs := Observer(reg)

	obs.ObserveRecipeRun("execute", "linear", "ok", 100*time.Millisecond)
	obs.ObserveRecipeStep("command", "ok", 50*time.Millisecond, 3)
	obs.ObserveRecipeHostResult("ok")
	obs.ObservePluginExec("echo", "noop", "ok", -1)
	obs.ObservePluginExecDuration("echo", "noop", 10*time.Millisecond)
	obs.ObserveSSHOperation("exec", "ok", 20*time.Millisecond)
	obs.ObserveAgentTransfer("ok", 500*time.Millisecond)
	obs.ObserveRecipeValidate("ok", 5*time.Millisecond)
	obs.ObserveExecCommand("ok", 4, 200*time.Millisecond)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	want := []string{
		"honey_recipe_runs_total",
		"honey_recipe_run_duration_seconds",
		"honey_recipe_steps_total",
		"honey_recipe_step_duration_seconds",
		"honey_recipe_step_retry_attempts_total",
		"honey_recipe_host_results_total",
		"honey_plugin_executions_total",
		"honey_plugin_execution_duration_seconds",
		"honey_ssh_operations_total",
		"honey_ssh_operation_duration_seconds",
		"honey_agent_transfers_total",
		"honey_agent_transfer_duration_seconds",
		"honey_recipe_validate_total",
		"honey_recipe_validate_duration_seconds",
		"honey_exec_commands_total",
		"honey_exec_command_duration_seconds",
		"honey_exec_command_hosts",
	}
	for _, name := range want {
		if !strings.Contains(text, name) {
			t.Errorf("metrics body missing %q", name)
		}
	}
}

func TestObserveRecipeStepNoRetryCounterWhenSingleAttempt(t *testing.T) {
	reg := NewRegistry("test", "abc123")
	reg.ObserveRecipeStep("command", "ok", time.Millisecond, 1)

	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Body)
	text := string(body)
	if strings.Contains(text, `honey_recipe_step_retry_attempts_total{kind="command"} 2`) {
		t.Error("expected no retry counter increment for single attempt")
	}
}
