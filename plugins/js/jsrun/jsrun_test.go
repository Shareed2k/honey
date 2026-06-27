package jsrun

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeHost is a test HostAPI: it records calls and returns canned data.
type fakeHost struct {
	lastScript string
	remote     RemoteResult
	store      map[string]string
	logs       []string
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		remote: RemoteResult{Stdout: "hi", ExitCode: 0},
		store:  map[string]string{},
	}
}

func (f *fakeHost) RemoteExec(script string) RemoteResult {
	f.lastScript = script
	return f.remote
}

func (f *fakeHost) KVGet(key string) (string, bool, error) {
	v, ok := f.store[key]
	return v, ok, nil
}

func (f *fakeHost) KVPut(key, value string) error {
	f.store[key] = value
	return nil
}

func (f *fakeHost) Log(msg string) { f.logs = append(f.logs, msg) }

func mustRun(t *testing.T, code string, args map[string]any, host HostAPI) Result {
	t.Helper()
	res, err := Run(context.Background(), code, args, host, time.Second)
	if err != nil {
		t.Fatalf("Run(%q) error: %v", code, err)
	}
	return res
}

func TestRun_returnsNumberAsJSON(t *testing.T) {
	res := mustRun(t, "1 + 1", nil, newFakeHost())
	if res.JSON != "2" {
		t.Fatalf("JSON=%q want %q", res.JSON, "2")
	}
}

func TestRun_returnsObjectAsJSON(t *testing.T) {
	res := mustRun(t, "({a: 1, b: 'x'})", nil, newFakeHost())
	// goja exports a JS object to map[string]any; JSON keys are sorted.
	if res.JSON != `{"a":1,"b":"x"}` {
		t.Fatalf("JSON=%q", res.JSON)
	}
}

func TestRun_stringNotDoubleEncoded(t *testing.T) {
	res := mustRun(t, "'hello'", nil, newFakeHost())
	if res.JSON != "hello" {
		t.Fatalf("JSON=%q want %q (string must pass through)", res.JSON, "hello")
	}
}

func TestRun_undefinedReturnsEmptyJSON(t *testing.T) {
	res := mustRun(t, "let x = 1;", nil, newFakeHost())
	if res.JSON != "" {
		t.Fatalf("JSON=%q want empty", res.JSON)
	}
}

func TestRun_argsBound(t *testing.T) {
	res := mustRun(t, "args.x * 2", map[string]any{"x": 21}, newFakeHost())
	if res.JSON != "42" {
		t.Fatalf("JSON=%q want 42", res.JSON)
	}
}

func TestRun_hostRemoteExec(t *testing.T) {
	h := newFakeHost()
	h.remote = RemoteResult{Stdout: "4", ExitCode: 0, Changed: true}
	res := mustRun(t, "host.remote_exec('nproc').stdout", nil, h)
	if h.lastScript != "nproc" {
		t.Fatalf("script=%q want nproc", h.lastScript)
	}
	if res.JSON != "4" {
		t.Fatalf("JSON=%q want 4", res.JSON)
	}
}

func TestRun_hostRemoteExecReturnsFields(t *testing.T) {
	h := newFakeHost()
	h.remote = RemoteResult{Stdout: "out", Stderr: "err", ExitCode: 3, Failed: true}
	res := mustRun(t, "let r = host.remote_exec('x'); JSON.stringify([r.exit_code, r.failed, r.stderr])", nil, h)
	if res.JSON != `[3,true,"err"]` {
		t.Fatalf("JSON=%q", res.JSON)
	}
}

func TestRun_kvRoundTrip(t *testing.T) {
	h := newFakeHost()
	res := mustRun(t, "kv.put('k', 'v'); kv.get('k')", nil, h)
	if res.JSON != "v" {
		t.Fatalf("JSON=%q want v", res.JSON)
	}
	if h.store["k"] != "v" {
		t.Fatalf("store not written: %v", h.store)
	}
}

func TestRun_kvGetMissingIsUndefined(t *testing.T) {
	res := mustRun(t, "kv.get('absent') === undefined", nil, newFakeHost())
	if res.JSON != "true" {
		t.Fatalf("JSON=%q want true", res.JSON)
	}
}

func TestRun_log(t *testing.T) {
	h := newFakeHost()
	mustRun(t, "log('hello'); log('world')", nil, h)
	if len(h.logs) != 2 || h.logs[0] != "hello" || h.logs[1] != "world" {
		t.Fatalf("logs=%v", h.logs)
	}
}

func TestRun_timeout(t *testing.T) {
	_, err := Run(context.Background(), "while (true) {}", nil, newFakeHost(), 50*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err=%v want ErrTimeout", err)
	}
}

func TestRun_contextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, "while (true) {}", nil, newFakeHost(), 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v want context.Canceled", err)
	}
}

func TestRun_jsRuntimeError(t *testing.T) {
	_, err := Run(context.Background(), "throw new Error('boom')", nil, newFakeHost(), time.Second)
	if err == nil {
		t.Fatal("expected error from thrown exception")
	}
}

func TestRun_jsSyntaxError(t *testing.T) {
	_, err := Run(context.Background(), "this is not js", nil, newFakeHost(), time.Second)
	if err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestRun_nilHost(t *testing.T) {
	_, err := Run(context.Background(), "1", nil, nil, time.Second)
	if err == nil {
		t.Fatal("expected error for nil host")
	}
}
