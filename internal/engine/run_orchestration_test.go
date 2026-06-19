package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/postgres"
)

func TestCueStepAllTargetsTransientTransportFailed(t *testing.T) {
	t.Parallel()
	if CueStepAllTargetsTransientTransportFailed(nil) {
		t.Fatal("expected false for nil slice")
	}
	if CueStepAllTargetsTransientTransportFailed([]HostExecResult{}) {
		t.Fatal("expected false for empty slice")
	}
	if CueStepAllTargetsTransientTransportFailed([]HostExecResult{{Success: true, ErrMsg: ""}}) {
		t.Fatal("expected false when any host succeeded")
	}
	if CueStepAllTargetsTransientTransportFailed([]HostExecResult{
		{Success: false, ErrMsg: "connection reset by peer"},
		{Success: false, ErrMsg: "exit 1"},
	}) {
		t.Fatal("expected false when failures are not all transport-class")
	}
	if !CueStepAllTargetsTransientTransportFailed([]HostExecResult{
		{Success: false, ErrMsg: "connection reset by peer"},
		{Success: false, ErrMsg: "read tcp 10.0.0.1:22: i/o timeout"},
	}) {
		t.Fatal("expected true when every host failed with transport-class errors")
	}
}

func TestCueRecipeSSHPostHostResult_CheckCmd(t *testing.T) {
	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			CheckCmd: "test -f /etc/ready",
		},
	}
	run := &CueRun{}
	post := CueRecipeSSHPostHostResult(context.TODO(), run, 0, cuetry.KindCommand, step, false)

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
	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe: cuetry.Recipe{
				Defaults: &cuetry.RecipeDefaults{
					GatherFacts: &gather,
				},
			},
			Records: []hosts.Record{
				{Name: "h1", PrimaryIP: "1.2.3.4"},
			},
		},
		Facts: make(map[string]map[string]any),
	}

	run.Facts["h1"] = map[string]any{
		"os":      "linux",
		"arch":    "amd64",
		"id":      "ubuntu",
		"pkg_mgr": "apt",
	}

	ctx := WithHostFacts(context.TODO(), run.Facts)
	retrieved := HostFactsFromContext(ctx)
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
	cache.SetRegistry(&MockRegistry{})
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
	cache.Clients()[key] = client

	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe: cuetry.Recipe{
				Handlers: wrapSteps(&cuetry.CommandStep{
					StepBase: cuetry.StepBase{
						ID:   "restart-app",
						Host: "h1",
					},
					Command: "echo restarting",
				}),
			},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		Cache:             cache,
		OutputStore:       cuetry.NewStepOutputStore(),
		TriggeredHandlers: make(map[string]bool),
	}

	// Record output for previous step to loop over
	run.OutputStore.Record("fetch-users", "h1", `[{"name":"alice"},{"name":"bob"}]`)

	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ID:   "process-users",
			Host: "h1",
			LoopFrom: &cuetry.RecipeLoop{
				Step:    "fetch-users",
				Extract: ".[].name",
			},
			NotifyHandler: []string{"restart-app"},
		},
		Command: "echo processed ${item}",
	}

	ch := make(chan HostExecResult, 10)
	results, err := StreamCueRecipeStep(context.TODO(), run, 0, step, nil, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Verify the handler trigger registration
	if len(run.TriggeredHandlers) != 1 || !run.TriggeredHandlers["restart-app"] {
		t.Errorf("expected restart-app handler to be triggered, got %v", run.TriggeredHandlers)
	}
}

func TestCueRun_loopTemplateMultilineStdout(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&MockRegistry{})

	var commands []string
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			commands = append(commands, cmd)
			return []byte("processed"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	key := SSHClientCacheKey("root", rec)
	cache.Clients()[key] = client

	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		Cache:       cache,
		OutputStore: cuetry.NewStepOutputStore(),
	}
	run.OutputStore.Record("get-controllers", "h1", "10.201.0.104\n10.201.0.22\n10.201.0.102\n")

	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ID:   "restart",
			Host: "h1",
			Loop: `{{ splitList "\n" (stepStdout "get-controllers") | compact | toJson }}`,
		},
		Command: `echo "${item}"`,
	}

	ch := make(chan HostExecResult, 10)
	results, err := StreamCueRecipeStep(context.TODO(), run, 0, step, nil, ch)
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
	cache.SetRegistry(&MockRegistry{})

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
		cache.Clients()[key] = client
	}

	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: records,
			SSHUser: "root",
		},
		Cache:       cache,
		OutputStore: cuetry.NewStepOutputStore(),
	}
	run.OutputStore.Record("get-controllers", "node-a", "node-a\nnode-b\n")

	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ID:   "restart",
			Host: "${item}",
			Loop: `{{ stepStdoutLines "get-controllers" | compact | toJson }}`,
		},
		Command: `echo "${item}"`,
	}

	ch := make(chan HostExecResult, 10)
	results, err := StreamCueRecipeStep(context.TODO(), run, 0, step, nil, ch)
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
	cache.SetRegistry(&MockRegistry{})
	client := &mockHostClient{
		runFunc: func(string) ([]byte, error) {
			return []byte("a\nb\n"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	cache.Clients()[SSHClientCacheKey("root", rec)] = client
	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{Type: "graph"},
			Records: []hosts.Record{rec},
			SSHUser: "root",
			Execute: true,
		},
		Cache:         cache,
		OutputStore:   cuetry.NewStepOutputStore(),
		OutputCapture: cuetry.NewRecipeOutputCapture(),
	}

	ch := make(chan HostExecResult, 10)
	_, err := StreamCueRecipeStep(context.TODO(), run, 0, &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ID:     "list",
			Host:   "h1",
			Output: "raw",
		},
		Command: "printf data",
	}, nil, ch)
	if err != nil {
		t.Fatal(err)
	}
	streamed := <-ch
	if streamed.OutputCapture != "raw" || CueRecipeDisplayOutput(streamed) != "[captured output: raw]" {
		t.Fatalf("streamed output marker missing: %+v", streamed)
	}
	if got, ok := run.OutputCapture.Get("raw"); !ok || got != "a\nb" {
		t.Fatalf("raw output = %q, ok=%v", got, ok)
	}

	_, err = StreamCueRecipeStep(context.TODO(), run, 1, &cuetry.TemplateStep{
		StepBase: cuetry.StepBase{
			ID:     "render",
			Host:   "_",
			Output: "items",
		},
		Render: `{{ .outputs.raw.stdout_lines | compact | toJson }}`,
	}, nil, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)
	if got, ok := run.OutputCapture.Get("items"); !ok || got != `["a","b"]` {
		t.Fatalf("items output = %q, ok=%v", got, ok)
	}
}

func TestCueRecipeSSHPostHostResult_changedWhenFailedWhen(t *testing.T) {
	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe: cuetry.Recipe{Name: "result-overrides"},
		},
		OutputStore:   cuetry.NewStepOutputStore(),
		OutputCapture: cuetry.NewRecipeOutputCapture(),
	}
	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ChangedWhen:   "false",
			FailedWhen:    `stdout.contains("bad")`,
			NotifyHandler: []string{"restart"},
		},
	}
	post := CueRecipeSSHPostHostResult(context.TODO(), run, 0, cuetry.KindCommand, step, false)
	res := &HostExecResult{Success: true, Output: "bad", ExitCode: 0}

	post(context.TODO(), hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}, res)

	if res.Success || res.ExitCode == 0 || !strings.Contains(res.ErrMsg, "failed_when") {
		t.Fatalf("result override failed: %+v", res)
	}
	if res.Changed {
		t.Fatalf("changed_when=false should clear changed: %+v", res)
	}
	if len(run.TriggeredHandlers) != 0 {
		t.Fatalf("handler should not trigger: %+v", run.TriggeredHandlers)
	}
}

func TestRecipeHostMaxConc_serialOverridesMaxParallel(t *testing.T) {
	got := RecipeHostMaxConc(&cuetry.CommandStep{RemoteExec: cuetry.RemoteExec{Serial: 1, MaxParallel: 8}}, nil)
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
			got := CueRecipeDisplayOutput(tt.res)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCueRun_stepTimeout(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&MockRegistry{})

	var lastCmd string
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			lastCmd = cmd
			return []byte("done"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	key := SSHClientCacheKey("root", rec)
	cache.Clients()[key] = client

	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		Cache:       cache,
		OutputStore: cuetry.NewStepOutputStore(),
	}

	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ID:      "test-timeout",
			Host:    "h1",
			Timeout: "5s",
		},
		Command: "sleep 10",
	}

	ch := make(chan HostExecResult, 10)
	_, err := StreamCueRecipeStep(context.TODO(), run, 0, step, nil, ch)
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
		Steps: wrapSteps(&cuetry.CommandStep{
			StepBase: cuetry.StepBase{
				ID:   "step-1",
				Host: "*",
			},
			Command: "echo hello",
		}),
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
		Steps: wrapSteps(&cuetry.CommandStep{
			StepBase: cuetry.StepBase{
				ID:   "step-1",
				Host: "host-1",
			},
			Command: "echo execute",
		}),
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

	reg := &MockRegistry{Client: client}

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
	cache.SetRegistry(&MockRegistry{})

	var commands []string
	client := &mockHostClient{
		runFunc: func(cmd string) ([]byte, error) {
			commands = append(commands, cmd)
			return []byte("ok"), nil
		},
	}

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	cache.Clients()[SSHClientCacheKey("root", rec)] = client

	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		Cache:       cache,
		OutputStore: cuetry.NewStepOutputStore(),
	}
	run.OutputStore.Record("get-items", "h1", "item1\nitem2\n")

	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ID:   "process",
			Host: "h1",
			Loop: `{{ stepStdoutLines "get-items" | compact | toJson }}`,
			Hooks: &cuetry.RecipeStepHooks{
				OnSuccess: &cuetry.RecipeStepHook{
					Where:   "local",
					Command: "false_command_does_not_exist",
				},
			},
		},
		Command: `echo "${item}"`,
	}

	ch := make(chan HostExecResult, 10)
	results, err := StreamCueRecipeStep(context.TODO(), run, 0, step, nil, ch)
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

func TestCueRun_opensearchStep(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" && r.URL.Path == "/my-index/_doc/doc1" {
			_, _ = w.Write([]byte(`{"_index":"my-index","_id":"doc1","found":true,"_source":{"message":"hello"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cache := NewClientCache()
	cache.SetRegistry(&MockRegistry{})

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}
	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe:  cuetry.Recipe{},
			Records: []hosts.Record{rec},
			SSHUser: "root",
		},
		Cache:         cache,
		OutputStore:   cuetry.NewStepOutputStore(),
		OutputCapture: cuetry.NewRecipeOutputCapture(),
	}

	step := &cuetry.OpensearchStep{
		StepBase: cuetry.StepBase{
			ID:   "get_doc",
			Host: "h1",
		},
		Opensearch: &cuetry.RecipeStepOpensearch{
			Addresses: []string{server.URL},
			Index:     "my-index",
			Action:    "get",
			DocID:     "doc1",
			Output:    "doc_result",
		},
	}

	ch := make(chan HostExecResult, 10)
	results, err := StreamCueRecipeStep(context.TODO(), run, 0, step, nil, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)

	if len(results) != 1 || !results[0].Success {
		t.Fatalf("unexpected step outcome: %+v", results)
	}

	captured, ok := run.OutputCapture.Get("doc_result")
	if !ok {
		t.Fatal("missing captured output 'doc_result'")
	}
	if !strings.Contains(captured, "hello") {
		t.Errorf("unexpected captured value: %q", captured)
	}
}

func TestCueRun_postgresStep(t *testing.T) {
	cache := NewClientCache()
	cache.SetRegistry(&MockRegistry{})

	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4"}

	// Create mock secret resolver
	mockResolver := &mockSecretResolver{
		resolveFunc: func(ref string) (string, error) {
			if strings.Contains(ref, "PG_DSN") || strings.Contains(ref, "secure:v1:test") {
				return "postgresql://postgres:password@localhost:5432/postgres", nil
			}
			return "", fmt.Errorf("not found")
		},
	}

	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe: cuetry.Recipe{
				Defaults: &cuetry.RecipeDefaults{
					Secrets: map[string]string{
						"PG_DSN": "secure:v1:test",
					},
				},
			},
			Records:        []hosts.Record{rec},
			SSHUser:        "root",
			SecretResolver: mockResolver,
			Pools:          postgres.NewPoolManager(),
		},
		Cache:         cache,
		OutputStore:   cuetry.NewStepOutputStore(),
		OutputCapture: cuetry.NewRecipeOutputCapture(),
	}

	step := &cuetry.PostgresStep{
		StepBase: cuetry.StepBase{
			ID:   "query_pg",
			Host: "h1",
		},
		Postgres: &cuetry.RecipeStepPostgres{
			DSNSecret: "PG_DSN",
			Action:    "query",
			SQL:       "SELECT 1 AS ok",
		},
	}

	run.Params.Execute = false

	ch := make(chan HostExecResult, 10)
	results, err := StreamCueRecipeStep(context.TODO(), run, 0, step, nil, ch)
	if err != nil {
		t.Fatal(err)
	}
	close(ch)

	if len(results) != 1 || !results[0].Success {
		t.Fatalf("unexpected step outcome: %+v", results)
	}
}

type mockSecretResolver struct {
	handlesFunc func(ref string) bool
	resolveFunc func(ref string) (string, error)
}

func (m *mockSecretResolver) Handles(ref string) bool {
	if m.handlesFunc != nil {
		return m.handlesFunc(ref)
	}
	return false
}

func (m *mockSecretResolver) Resolve(_ context.Context, ref string) (string, error) {
	if m.resolveFunc != nil {
		return m.resolveFunc(ref)
	}
	return "", fmt.Errorf("not implemented")
}

type fakeHostClient struct {
	closed bool
}

func (c *fakeHostClient) RunWithStreams(_ string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	return nil
}
func (c *fakeHostClient) Close() error               { c.closed = true; return nil }
func (c *fakeHostClient) Download(_, _ string) error { return nil }
func (c *fakeHostClient) Upload(_, _ string) error   { return nil }

func (c *fakeHostClient) ListRemoteDir(_ string) ([]hostexec.RemoteFileEntry, error) {
	return nil, nil
}

func (c *fakeHostClient) StatRemote(_ string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}

func (c *fakeHostClient) MkdirAllRemote(_ string) error { return nil }

func (c *fakeHostClient) RemoveRemote(_ string, _ bool) error { return nil }

func (c *fakeHostClient) InteractiveTerminal(_ string, _ map[string]string) error { return nil }

func (c *fakeHostClient) UploadContent(_ []byte, _ string, _ uint32) error { return nil }

func (c *fakeHostClient) Output(_ string, _ map[string]string) ([]byte, error) {
	return nil, nil
}

func (c *fakeHostClient) OutputWithStderr(_ string, _ map[string]string) ([]byte, []byte, error) {
	return nil, nil, nil
}

// StartLocalForward starts a local port forward.
func (m *mockHostClient) StartLocalForward(_ context.Context, _ string, _ int, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartRemoteForward starts a remote port forward.
func (m *mockHostClient) StartRemoteForward(_ context.Context, _ string, _ int, _ string, _ int) (remAddr string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartDynamicForward starts a dynamic port forward.
func (m *mockHostClient) StartDynamicForward(_ context.Context, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartUDPRelay starts a UDP relay.
func (m *mockHostClient) StartUDPRelay(_ context.Context, _ string, _ int, _ string, _ int, _ bool) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartTunForward starts a TUN forward.
func (m *mockHostClient) StartTunForward(_ context.Context, _ string, _ string, _ int, _, _ int) (tunName string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}
