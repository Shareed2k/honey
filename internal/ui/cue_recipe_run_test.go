package ui

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestCueStepAllTargetsTransientTransportFailed(t *testing.T) {
	t.Parallel()
	if cueStepAllTargetsTransientTransportFailed(nil) {
		t.Fatal("expected false for nil slice")
	}
	if cueStepAllTargetsTransientTransportFailed([]HostExecResult{}) {
		t.Fatal("expected false for empty slice")
	}
	if cueStepAllTargetsTransientTransportFailed([]HostExecResult{{Success: true, ErrMsg: ""}}) {
		t.Fatal("expected false when any host succeeded")
	}
	if cueStepAllTargetsTransientTransportFailed([]HostExecResult{
		{Success: false, ErrMsg: "connection reset by peer"},
		{Success: false, ErrMsg: "exit 1"},
	}) {
		t.Fatal("expected false when failures are not all transport-class")
	}
	if !cueStepAllTargetsTransientTransportFailed([]HostExecResult{
		{Success: false, ErrMsg: "connection reset by peer"},
		{Success: false, ErrMsg: "read tcp 10.0.0.1:22: i/o timeout"},
	}) {
		t.Fatal("expected true when every host failed with transport-class errors")
	}
}

func TestCueRecipeSSHPostHostResult_CheckCmd(t *testing.T) {
	step := cuetry.RecipeStep{
		CheckCmd: "test -f /etc/ready",
	}
	run := &cueRun{}
	post := cueRecipeSSHPostHostResult(context.TODO(), run, 0, cuetry.StepKindCommand, step, false)

	resChecked := HostExecResult{
		Output:  "HONEY_CHECK_CMD_OK",
		Success: true,
	}
	post(nil, hosts.Record{Name: "h1"}, &resChecked)

	if resChecked.Changed {
		t.Error("expected resChecked.Changed to be false when CheckCmd passes")
	}
	if resChecked.Output != "Skipped: check_cmd passed" {
		t.Errorf("expected clean output, got %q", resChecked.Output)
	}

	resMutated := HostExecResult{
		Output:  "some regular output",
		Success: true,
	}
	post(nil, hosts.Record{Name: "h1"}, &resMutated)

	if !resMutated.Changed {
		t.Error("expected resMutated.Changed to be true when main command executes")
	}
	if resMutated.Output != "some regular output" {
		t.Errorf("expected preserved output, got %q", resMutated.Output)
	}
}
