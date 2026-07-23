package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/client"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

const testDockerCueSource = `
actions: scan: {
	#Config: { target: string }
	argv: ["trivy", "image", "--format", "json", config.target]
	output_format: "json"
}
actions: broken: {
	#Config: {}
	argv: ["false"]
	output_format: "text"
}
actions: connect: {
	#Config: { host: string, password: string }
	argv: ["dbtool", "-h", config.host]
	env: { DB_PASSWORD: config.password }
	output_format: "text"
}
actions: search: {
	#Config: { url: string, query: string }
	argv: ["curl", "-sS", "--data-binary", "@-", config.url]
	stdin: config.query
	output_format: "json"
}
`

func newFakePluginInitServer(t *testing.T, handler func(req apiv1.ExecRequest) apiv1.ExecResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req apiv1.ExecRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("fake server: decode request: %v", err)
		}
		resp := handler(req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

//nolint:unparam // cueSource is parameterized for future test variants; today's suite only exercises testDockerCueSource
func newTestDockerTransport(t *testing.T, addr string, cueSource string) *dockerTransport {
	t.Helper()
	pc, err := newPluginCue([]byte(cueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	return &dockerTransport{addr: addr, cue: pc, httpClient: http.DefaultClient, started: true}
}

func TestDockerTransport_CallRaw_EnvReachesRequestNotArgv(t *testing.T) {
	var gotReq apiv1.ExecRequest
	srv := newFakePluginInitServer(t, func(req apiv1.ExecRequest) apiv1.ExecResponse {
		gotReq = req
		return apiv1.ExecResponse{Output: "connected", ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	inBytes, _ := json.Marshal(map[string]any{"host": "db.internal", "password": "s3cr3t"})
	if _, _, err := dt.CallRaw(context.Background(), "connect", inBytes); err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	for _, a := range gotReq.Argv {
		if a == "s3cr3t" {
			t.Fatalf("password leaked into argv: %v", gotReq.Argv)
		}
	}
	if gotReq.Env["DB_PASSWORD"] != "s3cr3t" {
		t.Fatalf("req.Env=%v want DB_PASSWORD=s3cr3t", gotReq.Env)
	}
}

func TestDockerTransport_CallRaw_StdinReachesRequestNotArgv(t *testing.T) {
	var gotReq apiv1.ExecRequest
	srv := newFakePluginInitServer(t, func(req apiv1.ExecRequest) apiv1.ExecResponse {
		gotReq = req
		return apiv1.ExecResponse{Output: `{"hits":{"total":0}}`, ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	body := `{"query":{"match_all":{}}}`
	inBytes, _ := json.Marshal(map[string]any{"url": "https://es.internal/_search", "query": body})
	if _, _, err := dt.CallRaw(context.Background(), "search", inBytes); err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	if string(gotReq.Stdin) != body {
		t.Fatalf("req.Stdin=%q want %q", gotReq.Stdin, body)
	}
	for _, a := range gotReq.Argv {
		if a == body {
			t.Fatalf("query body must not appear in argv: %v", gotReq.Argv)
		}
	}
}

func TestDockerTransport_CallRaw_JSONOutputPassedThrough(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Output: `{"vulnerabilities":[]}`, ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	inBytes, _ := json.Marshal(map[string]any{"target": "nginx:latest"})
	exit, out, err := dt.CallRaw(context.Background(), "scan", inBytes)
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	if exit != 0 {
		t.Fatalf("exit=%d want 0", exit)
	}
	if string(out) != `{"vulnerabilities":[]}` {
		t.Fatalf("out=%s", out)
	}
}

func TestDockerTransport_CallRaw_NonZeroExitBecomesPluginError(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Stderr: "boom", ExitCode: 1}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	exit, out, err := dt.CallRaw(context.Background(), "broken", []byte("{}"))
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	if exit == 0 {
		t.Fatal("expected nonzero exit")
	}
	var pe apiv1.PluginError
	if decErr := json.Unmarshal(out, &pe); decErr != nil {
		t.Fatalf("decode PluginError: %v", decErr)
	}
	if pe.Error != "boom" {
		t.Fatalf("PluginError.Error=%q want boom", pe.Error)
	}
}

func TestDockerTransport_CallRaw_ShimErrorBecomesPluginError(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Error: "exec: \"trivy\": executable file not found"}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	_, out, err := dt.CallRaw(context.Background(), "scan", []byte(`{"target":"x"}`))
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	var pe apiv1.PluginError
	if decErr := json.Unmarshal(out, &pe); decErr != nil {
		t.Fatalf("decode PluginError: %v", decErr)
	}
	if pe.Error == "" {
		t.Fatal("expected PluginError.Error to be set")
	}
}

func TestDockerTransport_CallRaw_TextOutputWrappedAsOutputField(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Output: "plain text result", ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	// "broken" action declares output_format: "text" in testDockerCueSource.
	_, out, err := dt.CallRaw(context.Background(), "broken", []byte("{}"))
	if err != nil {
		t.Fatalf("CallRaw: %v", err)
	}
	var wrapped struct {
		Output string `json:"output"`
	}
	if decErr := json.Unmarshal(out, &wrapped); decErr != nil {
		t.Fatalf("decode wrapped output: %v", decErr)
	}
	if wrapped.Output != "plain text result" {
		t.Fatalf("output=%q", wrapped.Output)
	}
}

func TestEnvSlice_SortedDeterministicOrder(t *testing.T) {
	got := envSlice(map[string]string{"B_VAR": "2", "A_VAR": "1"})
	want := []string{"A_VAR=1", "B_VAR=2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("envSlice=%v want=%v", got, want)
	}
}

func TestEnvSlice_Empty(t *testing.T) {
	if got := envSlice(nil); got != nil {
		t.Fatalf("envSlice(nil)=%v want nil", got)
	}
}

func TestBuildBinds_PluginInitFirstThenVolumes(t *testing.T) {
	got := buildBinds("/host/honey-plugin-init", []string{"/var/honey/media:/data:rw"})
	want := []string{
		"/host/honey-plugin-init:/honey-plugin-init:ro",
		"/var/honey/media:/data:rw",
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("binds=%v want=%v", got, want)
	}
}

func TestBuildBinds_NoVolumes(t *testing.T) {
	got := buildBinds("/host/honey-plugin-init", nil)
	want := []string{"/host/honey-plugin-init:/honey-plugin-init:ro"}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("binds=%v want=%v", got, want)
	}
}

func TestBuildBinds_BindModePrependsShim(t *testing.T) {
	got := buildBinds("/host/honey-plugin-init", []string{"/a:/b:ro"})
	if len(got) != 2 || got[0] != "/host/honey-plugin-init:"+pluginInitBindPath+":ro" {
		t.Fatalf("bind mode: got %v", got)
	}
	if got[1] != "/a:/b:ro" {
		t.Errorf("volume not preserved: %v", got)
	}
}

func TestBuildBinds_EmbeddedModeNoShim(t *testing.T) {
	got := buildBinds("", []string{"/a:/b:ro"})
	for _, b := range got {
		if strings.Contains(b, pluginInitBindPath) {
			t.Fatalf("embedded mode must not bind the shim, got %v", got)
		}
	}
	if len(got) != 1 || got[0] != "/a:/b:ro" {
		t.Errorf("embedded binds = %v, want just the volume", got)
	}
}

func TestEntrypointForMode(t *testing.T) {
	if ep := entrypointForMode("bind", "/ignored"); len(ep) != 1 || ep[0] != pluginInitBindPath {
		t.Errorf("bind entrypoint = %v, want %q", ep, pluginInitBindPath)
	}
	if ep := entrypointForMode("embedded", "/usr/local/bin/honey-plugin-init"); len(ep) != 1 || ep[0] != "/usr/local/bin/honey-plugin-init" {
		t.Errorf("embedded entrypoint = %v, want the init_path", ep)
	}
}

func TestDockerTransport_CallRaw_InvalidJSONOutputFormatFails(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Output: "not json", ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	if _, _, err := dt.CallRaw(context.Background(), "scan", []byte(`{"target":"x"}`)); err == nil {
		t.Fatal("expected error when output_format=json but output isn't valid JSON")
	}
}

// TestDockerTransport_CallRaw_ExecuteStepDecodesEnvelopeAndDispatchesAction
// proves that when export=="execute_step" (the recipe engine's real calling
// convention via Manager.ExecuteStep/step.go), CallRaw decodes inBytes as an
// apiv1.ExecuteStepInput envelope and dispatches the *inner* Action/Config
// against plugin.cue — not the literal string "execute_step" itself, which
// would fail with "unknown action" since no docker plugin defines an action
// named that.
func TestDockerTransport_CallRaw_ExecuteStepDecodesEnvelopeAndDispatchesAction(t *testing.T) {
	var gotReq apiv1.ExecRequest
	srv := newFakePluginInitServer(t, func(req apiv1.ExecRequest) apiv1.ExecResponse {
		gotReq = req
		return apiv1.ExecResponse{Output: `{"vulnerabilities":[]}`, ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	cfgBytes, err := json.Marshal(map[string]any{"target": "nginx:latest"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	stepIn := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		PluginID:   "trivy",
		Action:     "scan",
		Config:     cfgBytes,
	}
	inBytes, err := json.Marshal(stepIn)
	if err != nil {
		t.Fatalf("marshal ExecuteStepInput: %v", err)
	}

	exit, out, callErr := dt.CallRaw(context.Background(), "execute_step", inBytes)
	if callErr != nil {
		t.Fatalf("CallRaw: %v", callErr)
	}
	if exit != 0 {
		t.Fatalf("exit=%d want 0", exit)
	}

	wantArgv := []string{"trivy", "image", "--format", "json", "nginx:latest"}
	if len(gotReq.Argv) != len(wantArgv) {
		t.Fatalf("argv=%v want %v", gotReq.Argv, wantArgv)
	}
	for i, a := range wantArgv {
		if gotReq.Argv[i] != a {
			t.Fatalf("argv=%v want %v", gotReq.Argv, wantArgv)
		}
	}

	var stepOut apiv1.ExecuteStepOutput
	if decErr := json.Unmarshal(out, &stepOut); decErr != nil {
		t.Fatalf("decode ExecuteStepOutput: %v", decErr)
	}
	if !stepOut.Success {
		t.Fatal("Success=false want true")
	}
}

// TestDockerTransport_CallRaw_ExecuteStepNonZeroExitBecomesExecuteStepOutputNotError
// proves that a normal, expected step failure (the exec'd program ran and
// exited nonzero) flows through as ExecuteStepOutput{Success:false, ...}
// data, not a Go error — Manager.Call needs err==nil and exit==0 here so it
// decodes outBytes straight into the caller's *apiv1.ExecuteStepOutput
// instead of aborting the call.
func TestDockerTransport_CallRaw_ExecuteStepNonZeroExitBecomesExecuteStepOutputNotError(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Stderr: "boom", ExitCode: 7}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	stepIn := apiv1.ExecuteStepInput{Action: "broken", Config: []byte("{}")}
	inBytes, err := json.Marshal(stepIn)
	if err != nil {
		t.Fatalf("marshal ExecuteStepInput: %v", err)
	}

	exit, out, callErr := dt.CallRaw(context.Background(), "execute_step", inBytes)
	if callErr != nil {
		t.Fatalf("CallRaw: %v", callErr)
	}
	if exit != 0 {
		t.Fatalf("exit=%d want 0 (nonzero exec exit must not become a CallRaw-level failure)", exit)
	}

	var stepOut apiv1.ExecuteStepOutput
	if decErr := json.Unmarshal(out, &stepOut); decErr != nil {
		t.Fatalf("decode ExecuteStepOutput: %v", decErr)
	}
	if stepOut.Success {
		t.Fatal("Success=true want false")
	}
	if stepOut.ExitCode != 7 {
		t.Fatalf("ExitCode=%d want 7", stepOut.ExitCode)
	}
	if stepOut.Stderr != "boom" && stepOut.Err != "boom" {
		t.Fatalf("expected stderr text %q in Stderr or Err, got Stderr=%q Err=%q", "boom", stepOut.Stderr, stepOut.Err)
	}
}

// TestDockerTransport_CallRaw_ExecuteStepSuccessBuildsExecuteStepOutput proves
// the success path builds a proper ExecuteStepOutput{Success:true, ...}
// envelope rather than the direct-call convention's json-passthrough/
// {"output":...}-wrapped shapes.
func TestDockerTransport_CallRaw_ExecuteStepSuccessBuildsExecuteStepOutput(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Output: "connected", ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	cfgBytes, err := json.Marshal(map[string]any{"host": "db.internal", "password": "s3cr3t"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	stepIn := apiv1.ExecuteStepInput{Action: "connect", Config: cfgBytes}
	inBytes, err := json.Marshal(stepIn)
	if err != nil {
		t.Fatalf("marshal ExecuteStepInput: %v", err)
	}

	exit, out, callErr := dt.CallRaw(context.Background(), "execute_step", inBytes)
	if callErr != nil {
		t.Fatalf("CallRaw: %v", callErr)
	}
	if exit != 0 {
		t.Fatalf("exit=%d want 0", exit)
	}

	var stepOut apiv1.ExecuteStepOutput
	if decErr := json.Unmarshal(out, &stepOut); decErr != nil {
		t.Fatalf("decode ExecuteStepOutput: %v", decErr)
	}
	if !stepOut.Success {
		t.Fatal("Success=false want true")
	}
	if stepOut.Stdout != "connected" {
		t.Fatalf("Stdout=%q want %q", stepOut.Stdout, "connected")
	}
}

// TestDockerTransport_CallRaw_ExecuteStepShimErrorStillBecomesGoError proves
// that a shim-level failure (honey-plugin-init itself couldn't even exec the
// binary) is genuinely unchanged by the execute_step envelope handling: it
// still returns a nonzero exit with a decodable apiv1.PluginError, exactly
// like the pre-existing direct-call shim-error path
// (TestDockerTransport_CallRaw_ShimErrorBecomesPluginError) — this is an
// infrastructure failure, not step data, so it must keep surfacing as a Go
// error once Manager.Call sees it.
func TestDockerTransport_CallRaw_ExecuteStepShimErrorStillBecomesGoError(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Error: "exec: \"trivy\": executable file not found"}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	cfgBytes, err := json.Marshal(map[string]any{"target": "x"})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	stepIn := apiv1.ExecuteStepInput{Action: "scan", Config: cfgBytes}
	inBytes, err := json.Marshal(stepIn)
	if err != nil {
		t.Fatalf("marshal ExecuteStepInput: %v", err)
	}

	exit, out, callErr := dt.CallRaw(context.Background(), "execute_step", inBytes)
	if callErr != nil {
		t.Fatalf("CallRaw: %v", callErr)
	}
	if exit == 0 {
		t.Fatal("expected nonzero exit for a shim-level error")
	}
	var pe apiv1.PluginError
	if decErr := json.Unmarshal(out, &pe); decErr != nil {
		t.Fatalf("decode PluginError: %v", decErr)
	}
	if pe.Error == "" {
		t.Fatal("expected PluginError.Error to be set")
	}
}

// fakeCreateAndStart lets ensureStarted's lazy-first-use logic be tested
// without a real Docker daemon: same seam shape docker_restart_test.go
// already uses for restart's createAndStartFunc.
type fakeCreateAndStart struct {
	mu                    sync.Mutex
	calls                 int
	failuresBeforeSuccess int
}

func (f *fakeCreateAndStart) createAndStart(context.Context) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failuresBeforeSuccess {
		return "", "", errors.New("simulated create failure")
	}
	return "container-id", "http://container-addr.invalid:49094", nil
}

func (f *fakeCreateAndStart) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestDockerTransport_EnsureStarted_NotCalledAtConstruction(t *testing.T) {
	pc, err := newPluginCue([]byte(testDockerCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	dt := &dockerTransport{cue: pc, httpClient: http.DefaultClient}
	if dt.started {
		t.Fatal("expected started=false before any call — newDockerTransport must not create a container eagerly")
	}
	if dt.containerID != "" {
		t.Fatalf("containerID=%q, want empty before first use", dt.containerID)
	}
}

func TestDockerTransport_EnsureStarted_CreatesContainerOnlyOnce(t *testing.T) {
	pc, err := newPluginCue([]byte(testDockerCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	// onStarted intentionally left nil: ensureStarted's create/dedup logic is
	// under test here, not the real crash-watch goroutine (which calls the
	// live Docker API — see dockerTransport.onStarted's doc comment).
	dt := &dockerTransport{cue: pc, httpClient: http.DefaultClient, lifecycleCtx: context.Background()}
	fake := &fakeCreateAndStart{}

	for range 3 {
		if err := dt.ensureStarted(context.Background(), fake.createAndStart); err != nil {
			t.Fatalf("ensureStarted: %v", err)
		}
	}

	if got := fake.callCount(); got != 1 {
		t.Fatalf("createAndStart called %d times, want exactly 1", got)
	}
	if !dt.started {
		t.Fatal("expected started=true after a successful ensureStarted")
	}
	if dt.containerID != "container-id" || dt.addr != "http://container-addr.invalid:49094" {
		t.Fatalf("containerID=%q addr=%q not populated from createAndStart's result", dt.containerID, dt.addr)
	}
}

func TestDockerTransport_EnsureStarted_RetriesAfterFailureInsteadOfWedging(t *testing.T) {
	pc, err := newPluginCue([]byte(testDockerCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	dt := &dockerTransport{cue: pc, httpClient: http.DefaultClient, lifecycleCtx: context.Background()}
	fake := &fakeCreateAndStart{failuresBeforeSuccess: 1}

	if err := dt.ensureStarted(context.Background(), fake.createAndStart); err == nil {
		t.Fatal("expected the first (simulated failure) call to return an error")
	}
	if dt.started {
		t.Fatal("expected started=false after a failed attempt")
	}

	if err := dt.ensureStarted(context.Background(), fake.createAndStart); err != nil {
		t.Fatalf("expected the retry to succeed, got: %v", err)
	}
	if !dt.started {
		t.Fatal("expected started=true after the retry succeeds")
	}
	if got := fake.callCount(); got != 2 {
		t.Fatalf("createAndStart called %d times, want 2 (1 failure + 1 success)", got)
	}
}

func TestDockerTransport_EnsureStarted_ConcurrentCallsCreateOnlyOneContainer(t *testing.T) {
	pc, err := newPluginCue([]byte(testDockerCueSource))
	if err != nil {
		t.Fatalf("newPluginCue: %v", err)
	}
	dt := &dockerTransport{cue: pc, httpClient: http.DefaultClient, lifecycleCtx: context.Background()}
	fake := &fakeCreateAndStart{}

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_ = dt.ensureStarted(context.Background(), fake.createAndStart)
		})
	}
	wg.Wait()

	if got := fake.callCount(); got != 1 {
		t.Fatalf("createAndStart called %d times under concurrent first use, want exactly 1", got)
	}
}

func TestDockerTransport_Close_NeverStartedIsNoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dt := &dockerTransport{backend: &localBackend{cli: &client.Client{}}, cancel: cancel, lifecycleCtx: ctx}

	if err := dt.Close(context.Background()); err != nil {
		t.Fatalf("Close on a never-started transport should be a no-op, got: %v", err)
	}
}

func TestPollUntilReady_SucceedsWithinDeadline(t *testing.T) {
	attempts := 0
	checkFn := func() bool {
		attempts++
		return attempts >= 3
	}
	err := pollUntilReady(context.Background(), time.Now().Add(time.Second), checkFn)
	if err != nil {
		t.Fatalf("pollUntilReady: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts=%d want 3", attempts)
	}
}

func TestPollUntilReady_TimesOutIfNeverReady(t *testing.T) {
	checkFn := func() bool { return false }
	err := pollUntilReady(context.Background(), time.Now().Add(50*time.Millisecond), checkFn)
	if err == nil {
		t.Fatal("expected timeout error when checkFn never returns true")
	}
}

func TestPollUntilReady_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checkFn := func() bool { return false }
	err := pollUntilReady(ctx, time.Now().Add(time.Minute), checkFn)
	if err == nil {
		t.Fatal("expected error when context is already cancelled")
	}
}

// fakeContainerRemover is a minimal containerRemover double so
// cleanupFailedContainer's and stopAndRemoveContainer's ContainerRemove call
// can be exercised without a real Docker daemon.
type fakeContainerRemover struct {
	removeErr    error
	removeCalled bool
	removedID    string
}

func (f *fakeContainerRemover) ContainerRemove(_ context.Context, containerID string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.removeCalled = true
	f.removedID = containerID
	return client.ContainerRemoveResult{}, f.removeErr
}

// TestCleanupFailedContainer_RemovesContainerAndReturnsOriginalCause proves
// createAndStart's failure-path cleanup (Fix C) actually calls ContainerRemove
// for the container that was just created, and that on successful removal the
// original cause (why ContainerStart/waitForReady failed) is what's returned
// — this is the exact leak createAndStart used to have: docker_restart.go's
// crash-restart loop retries a broken plugin forever, so skipping this
// cleanup leaked one container per failed attempt, unboundedly.
func TestCleanupFailedContainer_RemovesContainerAndReturnsOriginalCause(t *testing.T) {
	remover := &fakeContainerRemover{}
	cause := errors.New("start container: boom")

	got := cleanupFailedContainer(context.Background(), remover, "container-123", cause)

	if !remover.removeCalled {
		t.Fatal("expected ContainerRemove to be called")
	}
	if remover.removedID != "container-123" {
		t.Fatalf("removedID=%q want container-123", remover.removedID)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("got=%v want original cause %v", got, cause)
	}
}

// TestCleanupFailedContainer_ReturnsOriginalCauseWhenRemovalItselfFails
// proves removal is best-effort: if ContainerRemove itself fails, that
// failure must not mask the original cause the caller actually needs to see.
func TestCleanupFailedContainer_ReturnsOriginalCauseWhenRemovalItselfFails(t *testing.T) {
	remover := &fakeContainerRemover{removeErr: errors.New("remove also failed")}
	cause := errors.New("waitForReady: timeout")

	got := cleanupFailedContainer(context.Background(), remover, "container-456", cause)

	if !remover.removeCalled {
		t.Fatal("expected ContainerRemove to be called")
	}
	if !errors.Is(got, cause) {
		t.Fatalf("got=%v want original cause %v (removal failure must not mask it)", got, cause)
	}
	if strings.Contains(got.Error(), "remove also failed") {
		t.Fatalf("got=%v should not surface the removal error to the caller", got)
	}
}

// fakeStopRemoveClient is a minimal containerStopper+containerRemover double
// so Close's stop-then-remove aggregation (Fix C) can be exercised without a
// real Docker daemon.
type fakeStopRemoveClient struct {
	stopErr      error
	removeErr    error
	removeCalled bool
}

func (f *fakeStopRemoveClient) ContainerStop(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error) {
	return client.ContainerStopResult{}, f.stopErr
}

func (f *fakeStopRemoveClient) ContainerRemove(_ context.Context, _ string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.removeCalled = true
	return client.ContainerRemoveResult{}, f.removeErr
}

// TestStopAndRemoveContainer_RemovesEvenWhenStopFails proves Close no longer
// skips ContainerRemove just because ContainerStop returned an error (the
// pre-fix behavior: an early return on ContainerStop's error left the
// container never removed at all).
func TestStopAndRemoveContainer_RemovesEvenWhenStopFails(t *testing.T) {
	fake := &fakeStopRemoveClient{stopErr: errors.New("stop failed")}

	err := stopAndRemoveContainer(context.Background(), fake, fake, "container-789")

	if !fake.removeCalled {
		t.Fatal("expected ContainerRemove to be called even though ContainerStop failed")
	}
	if err == nil || !strings.Contains(err.Error(), "stop failed") {
		t.Fatalf("err=%v want it to report the stop failure", err)
	}
}

// TestStopAndRemoveContainer_AggregatesBothErrors proves that when both
// ContainerStop and ContainerRemove fail, neither failure is silently
// dropped.
func TestStopAndRemoveContainer_AggregatesBothErrors(t *testing.T) {
	fake := &fakeStopRemoveClient{stopErr: errors.New("stop failed"), removeErr: errors.New("remove failed")}

	err := stopAndRemoveContainer(context.Background(), fake, fake, "container-789")

	if err == nil {
		t.Fatal("expected an aggregated error")
	}
	if !strings.Contains(err.Error(), "stop failed") || !strings.Contains(err.Error(), "remove failed") {
		t.Fatalf("err=%v want both failures present", err)
	}
}

// TestStopAndRemoveContainer_NoErrorsReturnsNil proves the happy path returns
// nil rather than a non-nil errors.Join wrapper around two nils.
func TestStopAndRemoveContainer_NoErrorsReturnsNil(t *testing.T) {
	fake := &fakeStopRemoveClient{}

	if err := stopAndRemoveContainer(context.Background(), fake, fake, "container-789"); err != nil {
		t.Fatalf("err=%v want nil", err)
	}
}
