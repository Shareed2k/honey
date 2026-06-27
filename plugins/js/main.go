//go:build wasip1 || wasm

// js is a honey WASM plugin that runs a user JavaScript snippet in an embedded
// goja interpreter, exposing a capability-gated host API (remote_exec, kv, log).
//
// Action "run" config:
//
//	{ "code": "<javascript>", "args": {<object>}, "timeout_ms": <int> }
//
// The script's completion value becomes the step's stdout (a string passes
// through verbatim, anything else is JSON-encoded) for env_from / loop_from.
// Without --execute the step runs in dry-run: the script is evaluated but
// side-effecting host calls (remote_exec, kv.put) are no-ops.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
package main

import (
	"context"
	"strings"
	"time"

	"github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
	"github.com/shareed2k/honey/plugins/js/jsrun"
)

const defaultTimeout = 30 * time.Second

type executeStepInput struct {
	Action  string `json:"action"`
	Config  []byte `json:"config,omitempty"`
	Execute bool   `json:"execute"`
}

type executeStepOutput struct {
	Success bool   `json:"success"`
	Changed bool   `json:"changed,omitempty"`
	Stdout  string `json:"stdout,omitempty"`
	Err     string `json:"err,omitempty"`
}

type runConfig struct {
	Code      string         `json:"code"`
	Args      map[string]any `json:"args,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
}

// liveHost bridges the script's host API to honey host functions.
type liveHost struct{}

func (liveHost) RemoteExec(script string) jsrun.RemoteResult {
	out, err := pluginpdk.RemoteExec("/bin/sh", script)
	if err != nil {
		return jsrun.RemoteResult{Failed: true, Error: err.Error()}
	}
	return jsrun.RemoteResult{
		Stdout:   out.Stdout,
		Stderr:   out.Stderr,
		ExitCode: out.ExitCode,
		Failed:   out.Failed,
		Changed:  out.Changed,
		Error:    out.Error,
	}
}

func (liveHost) KVGet(key string) (string, bool, error) { return pluginpdk.KVGet(key) }
func (liveHost) KVPut(key, value string) error          { return pluginpdk.KVPut(key, value) }
func (liveHost) Log(msg string)                         { pdk.Log(pdk.LogInfo, msg) }

// dryHost evaluates the script without side effects: writes become no-ops,
// reads stay available so logic can branch.
type dryHost struct{}

func (dryHost) RemoteExec(string) jsrun.RemoteResult   { return jsrun.RemoteResult{Changed: true} }
func (dryHost) KVGet(key string) (string, bool, error) { return pluginpdk.KVGet(key) }
func (dryHost) KVPut(string, string) error             { return nil }
func (dryHost) Log(msg string)                         { pdk.Log(pdk.LogInfo, msg) }

func main() {}

//go:wasmexport execute_step
func executeStep() int32 {
	var in executeStepInput
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	if strings.TrimSpace(in.Action) != "run" {
		return writeOutput(executeStepOutput{Success: false, Err: "unknown action " + in.Action})
	}

	cfg, err := pluginpdk.ReadConfig[runConfig](in.Config)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	if strings.TrimSpace(cfg.Code) == "" {
		return writeOutput(executeStepOutput{Success: false, Err: "config.code is required"})
	}

	timeout := defaultTimeout
	if cfg.TimeoutMS > 0 {
		timeout = time.Duration(cfg.TimeoutMS) * time.Millisecond
	}

	var host jsrun.HostAPI = liveHost{}
	if !in.Execute {
		host = dryHost{}
	}

	res, err := jsrun.Run(context.Background(), cfg.Code, cfg.Args, host, timeout)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: res.JSON})
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
