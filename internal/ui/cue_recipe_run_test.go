package ui

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestCueRun_loopTemplateMultilineStdout(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&hostexec.StandardRegistry{})

	var commands []string
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			commands = append(commands, cmd)
			return []byte("processed"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	key := SSHClientCacheKey("root", rec)
	cache.clients[key] = client

	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		cache:       cache,
		outputStore: cuetry.NewStepOutputStore(),
	}
	run.outputStore.Record("get-controllers", "h1", "10.201.0.104\n10.201.0.22\n10.201.0.102\n")

	step := cuetry.RecipeStep{
		ID:      "restart",
		Host:    "h1",
		Loop:    `{{ splitList "\n" (stepStdout "get-controllers") | compact | toJson }}`,
		Command: `echo "${item}"`,
	}

	ch := make(chan HostExecResult, 10)
	results, err := streamCueRecipeStep(context.TODO(), run, 0, step, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	for _, want := range []string{"10.201.0.104", "10.201.0.22", "10.201.0.102"} {
		found := false
		for _, cmd := range commands {
			if strings.Contains(cmd, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected command containing %q, got %+v", want, commands)
		}
	}
}

func TestCueRun_loopTemplateHostItem(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&hostexec.StandardRegistry{})

	var commands []string
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			commands = append(commands, cmd)
			return []byte("processed"), nil
		},
	}

	records := []hosts.Record{
		{Name: "node-a", PrimaryIP: "10.0.0.1"},
		{Name: "node-b", PrimaryIP: "10.0.0.2"},
	}
	for _, rec := range records {
		key := SSHClientCacheKey("root", rec)
		cache.clients[key] = client
	}

	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: records,
			SSHUser: "root",
		},
		cache:       cache,
		outputStore: cuetry.NewStepOutputStore(),
	}
	run.outputStore.Record("get-controllers", "node-a", "node-a\nnode-b\n")

	step := cuetry.RecipeStep{
		ID:      "restart",
		Host:    "${item}",
		Loop:    `{{ stepStdoutLines "get-controllers" | compact | toJson }}`,
		Command: `echo "${item}"`,
	}

	ch := make(chan HostExecResult, 10)
	results, err := streamCueRecipeStep(context.TODO(), run, 0, step, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, want := range []string{"node-a", "node-b"} {
		found := false
		for _, cmd := range commands {
			if strings.Contains(cmd, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected command containing %q, got %+v", want, commands)
		}
	}
}

func TestCueRun_stepOutputCaptureAndRender(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&hostexec.StandardRegistry{})
	client := &mockHostClient{
		runFunc: func(string) ([]byte, error) {
			return []byte("a\nb\n"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	cache.clients[SSHClientCacheKey("root", rec)] = client
	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{Type: "graph"},
			Records: []hosts.Record{rec},
			SSHUser: "root",
			Execute: true,
		},
		cache:         cache,
		outputStore:   cuetry.NewStepOutputStore(),
		outputCapture: cuetry.NewRecipeOutputCapture(),
	}

	ch := make(chan HostExecResult, 10)
	_, err := streamCueRecipeStep(context.TODO(), run, 0, cuetry.RecipeStep{
		ID:      "list",
		Host:    "h1",
		Command: "printf data",
		Output:  "raw",
	}, ch)
	if err != nil {
		t.Fatal(err)
	}
	streamed := <-ch
	if streamed.OutputCapture != "raw" || cueRecipeDisplayOutput(streamed) != "[captured output: raw]" {
		t.Fatalf("streamed output marker missing: %+v", streamed)
	}
	if got, ok := run.outputCapture.Get("raw"); !ok || got != "a\nb" {
		t.Fatalf("raw output = %q, ok=%v", got, ok)
	}

	_, err = streamCueRecipeStep(context.TODO(), run, 1, cuetry.RecipeStep{
		ID:     "render",
		Host:   "_",
		Render: `{{ .outputs.raw.stdout_lines | compact | toJson }}`,
		Output: "items",
	}, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)
	if got, ok := run.outputCapture.Get("items"); !ok || got != `["a","b"]` {
		t.Fatalf("items output = %q, ok=%v", got, ok)
	}
}

func TestCueRecipeSSHPostHostResult_changedWhenFailedWhen(t *testing.T) {
	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe: cuetry.Recipe{Name: "result-overrides"},
		},
		outputStore:   cuetry.NewStepOutputStore(),
		outputCapture: cuetry.NewRecipeOutputCapture(),
	}
	step := cuetry.RecipeStep{
		ChangedWhen:   "false",
		FailedWhen:    `stdout.contains("bad")`,
		NotifyHandler: []string{"restart"},
	}
	post := cueRecipeSSHPostHostResult(context.TODO(), run, 0, cuetry.StepKindCommand, step, false)
	res := &HostExecResult{Success: true, Output: "bad", ExitCode: 0}

	post(context.TODO(), hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}, res)

	if res.Success || res.ExitCode == 0 || !strings.Contains(res.ErrMsg, "failed_when") {
		t.Fatalf("result override failed: %+v", res)
	}
	if res.Changed {
		t.Fatalf("changed_when=false should clear changed: %+v", res)
	}
	if len(run.triggeredHandlers) != 0 {
		t.Fatalf("handler should not trigger: %+v", run.triggeredHandlers)
	}
}

func TestRecipeHostMaxConc_serialOverridesMaxParallel(t *testing.T) {
	got := recipeHostMaxConc(cuetry.RecipeStep{Serial: 1, MaxParallel: 8}, nil)
	if got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
}

func TestCueRecipeDisplayOutput_suppressesSuccessfulCapturedOutput(t *testing.T) {
	tests := []struct {
		name string
		res  HostExecResult
		want string
	}{
		{
			name: "successful captured output",
			res:  HostExecResult{Success: true, Output: "a\nb", OutputCapture: "controllers_raw"},
			want: "[captured output: controllers_raw]",
		},
		{
			name: "failed captured output remains visible",
			res:  HostExecResult{Success: false, Output: "error details", OutputCapture: "controllers_raw"},
			want: "error details",
		},
		{
			name: "uncaptured output remains visible",
			res:  HostExecResult{Success: true, Output: "hello"},
			want: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cueRecipeDisplayOutput(tt.res)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCueRun_stepTimeout(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&hostexec.StandardRegistry{})

	var lastCmd string
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			lastCmd = cmd
			return []byte("done"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	key := SSHClientCacheKey("root", rec)
	cache.clients[key] = client

	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		cache:       cache,
		outputStore: cuetry.NewStepOutputStore(),
	}

	step := cuetry.RecipeStep{
		ID:      "test-timeout",
		Host:    "h1",
		Command: "sleep 10",
		Timeout: "5s",
	}

	ch := make(chan HostExecResult, 10)
	_, err := streamCueRecipeStep(context.TODO(), run, 0, step, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)

	if !strings.Contains(lastCmd, "timeout 5s") {
		t.Errorf("expected command to be wrapped with timeout, got: %q", lastCmd)
	}
}

func TestRunCueRecipeStepsJSON_DryRun(t *testing.T) {
	recipe := cuetry.Recipe{
		Name: "test-dry-run",
		Steps: []cuetry.RecipeStep{
			{
				ID:      "step-1",
				Host:    "*",
				Command: "echo hello",
			},
		},
	}
	records := []hosts.Record{
		{
			Name:      "host-1",
			PrimaryIP: "1.2.3.4",
			Provider:  "gcp",
		},
	}

	var buf bytes.Buffer
	p := CueRecipeRunParams{
		Recipe:    recipe,
		Records:   records,
		Execute:   false,
		JSON:      true,
		SSHUser:   "root",
		PluginMgr: nil,
	}

	err := RunCueRecipeSteps(context.Background(), &buf, p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v, output: %s", err, buf.String())
	}

	plan, ok := resp["plan"]
	if !ok {
		t.Fatalf("missing 'plan' key in response")
	}

	if !strings.Contains(plan, "step 0:") {
		t.Errorf("expected plan to describe step 0, got: %q", plan)
	}
}

func TestRunCueRecipeStepsJSON_Execute(t *testing.T) {
	recipe := cuetry.Recipe{
		Name: "test-execute",
		Steps: []cuetry.RecipeStep{
			{
				ID:      "step-1",
				Host:    "host-1",
				Command: "echo execute",
			},
		},
	}
	rec := hosts.Record{
		Name:      "host-1",
		PrimaryIP: "1.2.3.4",
		Provider:  "gcp",
		Meta:      map[string]string{"ssh_user": "root"},
	}

	client := &mockHostClient{
		runFunc: func(_ string) ([]byte, error) {
			return []byte("run ok"), nil
		},
	}

	mockDialer := hostexec.DialerFunc(func(_, _ string, _ int, _ string) (hostexec.HostClient, error) {
		return client, nil
	})
	reg := &hostexec.StandardRegistry{
		Dialer: mockDialer,
	}

	var buf bytes.Buffer
	p := CueRecipeRunParams{
		Recipe:    recipe,
		Records:   []hosts.Record{rec},
		Execute:   true,
		JSON:      true,
		SSHUser:   "root",
		PluginMgr: nil,
		Reg:       reg,
	}

	err := RunCueRecipeSteps(context.Background(), &buf, p, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp map[string][]HostExecResult
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal JSON output: %v, output: %s", err, buf.String())
	}

	results, ok := resp["results"]
	if !ok {
		t.Fatalf("missing 'results' key in response")
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got: %d", len(results))
	}

	if results[0].Name != "Step 1 | host-1" {
		t.Errorf("unexpected name: %q", results[0].Name)
	}
	if !results[0].Success {
		t.Errorf("expected success to be true")
	}
}

func TestCueRun_loopAbortedOnHookFailure(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&hostexec.StandardRegistry{})

	var commands []string
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			commands = append(commands, cmd)
			return []byte("ok"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	cache.clients[SSHClientCacheKey("root", rec)] = client

	run := &cueRun{
		CueRecipeRunParams: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		cache:       cache,
		outputStore: cuetry.NewStepOutputStore(),
	}
	run.outputStore.Record("get-items", "h1", "item1\nitem2\n")

	step := cuetry.RecipeStep{
		ID:      "process",
		Host:    "h1",
		Loop:    `{{ stepStdoutLines "get-items" | compact | toJson }}`,
		Command: `echo "${item}"`,
		Hooks: &cuetry.RecipeStepHooks{
			OnSuccess: &cuetry.RecipeStepHook{
				Where:   "local",
				Command: "false_command_does_not_exist",
			},
		},
	}

	ch := make(chan HostExecResult, 10)
	results, err := streamCueRecipeStep(context.TODO(), run, 0, step, ch)
	close(ch)

	if err == nil {
		t.Fatal("expected error from loop step due to hook failure")
	}
	if !strings.Contains(err.Error(), "hook failed") {
		t.Errorf("expected error message to mention hook failure, got: %v", err)
	}

	// Only item1 should have executed because the hook failure on item1 aborts the loop before item2 runs
	if len(commands) != 1 {
		t.Errorf("expected only 1 command execution, got: %d", len(commands))
	}
	if len(results) != 1 {
		t.Errorf("expected only 1 result, got: %d", len(results))
	}
}
