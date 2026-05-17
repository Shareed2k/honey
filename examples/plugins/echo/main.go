package main

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
)

const apiVersion = "honey.plugins/v1"
const echoPrefix = "echo:"
const transformMarker = "// honey-echo-transform\n"

//go:wasmimport extism:host/user host_exec
func hostExec(inputOffset uint64) uint64

type cueTransformInput struct {
	APIVersion string `json:"api_version"`
	Cue        string `json:"cue"`
	HostsCount int    `json:"hosts_count"`
}

type cueTransformOutput struct {
	Cue string `json:"cue"`
}

type executeStepInput struct {
	APIVersion string `json:"api_version"`
	StepIndex  int    `json:"step_index"`
	Host       []byte `json:"host"`
	Env        map[string]string `json:"env,omitempty"`
	PluginID   string `json:"plugin_id"`
	Action     string `json:"action"`
	Config     []byte `json:"config,omitempty"`
	Execute    bool   `json:"execute"`
	SecretsDry bool   `json:"secrets_dry_run"`
}

type executeStepOutput struct {
	Success bool   `json:"success"`
	Stdout  string `json:"stdout,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
	Err     string `json:"err,omitempty"`
}

type resolveSecretInput struct {
	APIVersion string `json:"api_version"`
	Ref        string `json:"ref"`
	Label      string `json:"label,omitempty"`
	PluginID   string `json:"plugin_id"`
}

type resolveSecretOutput struct {
	Value string `json:"value"`
}

type hostExecInput struct {
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd,omitempty"`
	TimeoutMS int      `json:"timeout_ms,omitempty"`
}

type hostExecOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"`
}

func main() {}

//go:wasmexport cue_transform
func cueTransform() int32 {
	var in cueTransformInput
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	raw, err := base64.StdEncoding.DecodeString(in.Cue)
	if err != nil {
		pdk.SetErrorString("invalid cue base64")
		return 1
	}
	out := append([]byte(transformMarker), raw...)
	if err := pdk.OutputJSON(cueTransformOutput{Cue: base64.StdEncoding.EncodeToString(out)}); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

//go:wasmexport execute_step
func executeStep() int32 {
	var in executeStepInput
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	out := executeStepOutput{Success: true}
	if !in.Execute {
		out.Stdout = "dry-run"
		return writeExecuteOutput(out)
	}
	if strings.TrimSpace(in.Action) == "host_exec" {
		stdout, errMsg := runHostExec([]string{"echo", "ok"})
		if errMsg != "" {
			out.Success = false
			out.Err = errMsg
		} else {
			out.Stdout = stdout
		}
		return writeExecuteOutput(out)
	}
	if strings.TrimSpace(in.Action) == "kv_ping" {
		key := "echo-kv-ping"
		if err := pluginpdk.KVPut(key, "pong"); err != nil {
			out.Success = false
			out.Err = err.Error()
			return writeExecuteOutput(out)
		}
		val, found, err := pluginpdk.KVGet(key)
		if err != nil {
			out.Success = false
			out.Err = err.Error()
		} else if !found || val != "pong" {
			out.Success = false
			out.Err = "kv round-trip failed"
		} else {
			out.Stdout = val
		}
		return writeExecuteOutput(out)
	}
	out.Stdout = "executed"
	return writeExecuteOutput(out)
}

//go:wasmexport resolve_secret
func resolveSecret() int32 {
	var in resolveSecretInput
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	ref := strings.TrimSpace(in.Ref)
	if !strings.HasPrefix(ref, echoPrefix) {
		pdk.SetErrorString("ref missing echo: prefix")
		return 1
	}
	val := strings.TrimPrefix(ref, echoPrefix)
	if err := pdk.OutputJSON(resolveSecretOutput{Value: val}); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

func writeExecuteOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

func runHostExec(argv []string) (stdout, errMsg string) {
	in := hostExecInput{Argv: argv, TimeoutMS: 5000}
	mem, err := pdk.AllocateJSON(in)
	if err != nil {
		return "", err.Error()
	}
	off := hostExec(mem.Offset())
	if off == 0 {
		return "", "host_exec returned 0"
	}
	result := pdk.FindMemory(off)
	var out hostExecOutput
	if err := json.Unmarshal(result.ReadBytes(), &out); err != nil {
		return "", err.Error()
	}
	if out.Error != "" {
		return "", out.Error
	}
	if out.ExitCode != 0 {
		return "", "exit " + strconv.Itoa(out.ExitCode)
	}
	return strings.TrimSpace(out.Stdout), ""
}
