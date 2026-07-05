// Package jsrun holds the pure, host-testable core of the js plugin: it runs a
// user JavaScript snippet in an embedded goja interpreter, exposing a narrow,
// capability-gated host API (remote_exec, kv, log) injected by the caller. It
// imports nothing WASM-specific so it can be unit tested on the host toolchain.
package jsrun

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/dop251/goja"
)

// RemoteResult is the value returned to JS from host.remote_exec.
type RemoteResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Failed   bool
	Changed  bool
	Error    string
}

// HostAPI is the capability surface exposed to scripts. The plugin implements it
// over honey host functions; tests implement it with fakes.
type HostAPI interface {
	RemoteExec(script string) RemoteResult
	KVGet(key string) (value string, found bool, err error)
	KVPut(key, value string) error
	Log(msg string)
}

// Result is the outcome of running a script.
type Result struct {
	// Value is the script's completion value, exported to a Go value.
	Value any
	// JSON is Value rendered for the plugin's stdout: a string passes through
	// verbatim, anything else is JSON-encoded; undefined/null yields "".
	JSON string
}

// ErrTimeout is returned when the script exceeds its time budget.
var ErrTimeout = errors.New("jsrun: script timed out")

// Run executes code with args bound to a global `args` object and the host API
// bound to globals `host`, `kv`, and `log`. A zero timeout means no deadline.
func Run(ctx context.Context, code string, args map[string]any, host HostAPI, timeout time.Duration) (Result, error) {
	if host == nil {
		return Result{}, errors.New("jsrun: nil host api")
	}

	vm := goja.New()
	if err := bindHost(vm, host); err != nil {
		return Result{}, err
	}
	if args == nil {
		args = map[string]any{}
	}
	if err := vm.Set("args", args); err != nil {
		return Result{}, err
	}

	// Watch for timeout / context cancellation and interrupt the VM.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-timeoutChan(timeout):
			vm.Interrupt(ErrTimeout)
		case <-ctxDone(ctx):
			vm.Interrupt(context.Canceled)
		}
	}()

	val, err := vm.RunString(code)
	if err != nil {
		if ie, ok := errors.AsType[*goja.InterruptedError](err); ok {
			if ie.Value() == ErrTimeout {
				return Result{}, ErrTimeout
			}
			return Result{}, context.Canceled
		}
		return Result{}, err
	}

	res := Result{}
	if val != nil && !goja.IsUndefined(val) && !goja.IsNull(val) {
		res.Value = val.Export()
	}
	res.JSON = toJSON(res.Value)
	return res, nil
}

func bindHost(vm *goja.Runtime, host HostAPI) error {
	hostObj := map[string]any{
		"remote_exec": func(script string) map[string]any {
			r := host.RemoteExec(script)
			return map[string]any{
				"stdout":    r.Stdout,
				"stderr":    r.Stderr,
				"exit_code": r.ExitCode,
				"failed":    r.Failed,
				"changed":   r.Changed,
				"error":     r.Error,
			}
		},
	}
	if err := vm.Set("host", hostObj); err != nil {
		return err
	}

	kvObj := map[string]any{
		"get": func(key string) goja.Value {
			v, found, err := host.KVGet(key)
			if err != nil || !found {
				return goja.Undefined()
			}
			return vm.ToValue(v)
		},
		"put": func(key, value string) error {
			return host.KVPut(key, value)
		},
	}
	if err := vm.Set("kv", kvObj); err != nil {
		return err
	}

	return vm.Set("log", func(msg string) { host.Log(msg) })
}

// toJSON renders v for stdout: strings pass through verbatim so a script that
// already returns JSON text is not double-encoded.
func toJSON(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func timeoutChan(d time.Duration) <-chan time.Time {
	if d <= 0 {
		return nil // nil channel blocks forever
	}
	return time.After(d)
}

func ctxDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}
