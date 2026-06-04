package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
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

func TestCueRun_gatherFacts(t *testing.T) {
	gather := true
	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe: cuetry.Recipe{
				Defaults: &cuetry.RecipeDefaults{
					GatherFacts: &gather,
				},
			},
			Records: []hosts.Record{
				{Name: "h1", PrimaryIP: "1.2.3.4"},
			},
		},
		facts: make(map[string]map[string]any),
	}

	run.facts["h1"] = map[string]any{
		"os":      "linux",
		"arch":    "amd64",
		"id":      "ubuntu",
		"pkg_mgr": "apt",
	}

	ctx := withHostFacts(context.TODO(), run.facts)
	retrieved := hostFactsFromContext(ctx)
	if retrieved == nil || retrieved["h1"]["pkg_mgr"] != "apt" {
		t.Fatal("expected facts to be propagated via context")
	}
}

type mockHostClient struct {
	fakeHostClient
	runFunc func(string) ([]byte, error)
}

func (m *mockHostClient) SupportsKVTunnel() bool {
	return false
}

func (m *mockHostClient) Run(cmd string) ([]byte, error) {
	if m.runFunc != nil {
		return m.runFunc(cmd)
	}
	return nil, nil
}

func TestCueRun_loopFromAndHandlers(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&hostexec.StandardRegistry{})
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			if strings.Contains(cmd, "restarting") {
				return []byte("restarted"), nil
			}
			return []byte("processed"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	key := SSHClientCacheKey("root", rec)
	cache.clients[key] = client

	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe: cuetry.Recipe{
				Handlers: []cuetry.RecipeStep{
					{
						ID:      "restart-app",
						Host:    "h1",
						Command: "echo restarting",
					},
				},
			},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		cache:             cache,
		outputStore:       cuetry.NewStepOutputStore(),
		triggeredHandlers: make(map[string]bool),
	}

	// Record output for previous step to loop over
	run.outputStore.Record("fetch-users", "h1", `[{"name":"alice"},{"name":"bob"}]`)

	step := cuetry.RecipeStep{
		ID:      "process-users",
		Host:    "h1",
		Command: "echo processed ${item}",
		LoopFrom: &cuetry.RecipeLoop{
			Step:    "fetch-users",
			Extract: ".[].name",
		},
		NotifyHandler: []string{"restart-app"},
	}

	ch := make(chan HostExecResult, 10)
	results, err := streamCueRecipeStep(context.TODO(), run, 0, step, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Verify the handler trigger registration
	if len(run.triggeredHandlers) != 1 || !run.triggeredHandlers["restart-app"] {
		t.Errorf("expected restart-app handler to be triggered, got %v", run.triggeredHandlers)
	}
}
