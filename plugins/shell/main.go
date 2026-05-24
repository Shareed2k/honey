//go:build wasip1 || wasm

package main

import (
	"strings"

	"github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
)

type runConfig struct {
	Script string `json:"script"`
}

type executeStepInput struct {
	Action  string `json:"action"`
	Config  []byte `json:"config,omitempty"`
	Execute bool   `json:"execute"`
}

type executeStepOutput struct {
	Success  bool   `json:"success"`
	Changed  bool   `json:"changed,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Err      string `json:"err,omitempty"`
}

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
	script := strings.TrimSpace(cfg.Script)
	if script == "" {
		return writeOutput(executeStepOutput{Success: false, Err: "config.script is required"})
	}
	if !in.Execute {
		return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: "would run sh script"})
	}
	out, err := pluginpdk.RemoteExec("/bin/sh", script)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	if out.Error != "" {
		return writeOutput(executeStepOutput{Success: false, Changed: out.Changed, Err: out.Error, Stdout: out.Stdout, Stderr: out.Stderr, ExitCode: out.ExitCode})
	}
	success := !out.Failed && out.ExitCode == 0
	return writeOutput(executeStepOutput{
		Success:  success,
		Changed:  out.Changed,
		ExitCode: out.ExitCode,
		Stdout:   out.Stdout,
		Stderr:   out.Stderr,
	})
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
