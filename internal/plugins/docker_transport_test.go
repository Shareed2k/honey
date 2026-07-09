package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
	return &dockerTransport{addr: addr, cue: pc, httpClient: http.DefaultClient}
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

func TestDockerTransport_CallRaw_InvalidJSONOutputFormatFails(t *testing.T) {
	srv := newFakePluginInitServer(t, func(_ apiv1.ExecRequest) apiv1.ExecResponse {
		return apiv1.ExecResponse{Output: "not json", ExitCode: 0}
	})
	dt := newTestDockerTransport(t, srv.URL, testDockerCueSource)

	if _, _, err := dt.CallRaw(context.Background(), "scan", []byte(`{"target":"x"}`)); err == nil {
		t.Fatal("expected error when output_format=json but output isn't valid JSON")
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
