//go:build wasip1 || wasm

package main

import (
	"strings"

	pdk "github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
)

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
	cfg, err := pluginpdk.ReadConfig[helmConfig](in.Config)
	if err != nil {
		return writeOutput(executeStepOutput{Err: "read config: " + err.Error()})
	}

	act := strings.TrimSpace(in.Action)
	if act == "" {
		return writeOutput(executeStepOutput{Err: "action is required"})
	}

	argv, err := buildArgv(act, cfg)
	if err != nil {
		return writeOutput(executeStepOutput{Err: err.Error()})
	}

	if !in.Execute {
		return writeOutput(executeStepOutput{
			Success: true,
			Stdout:  "would run: " + strings.Join(argv, " "),
		})
	}

	out, err := pluginpdk.HostExec(argv, "", 300_000)
	if err != nil {
		return writeOutput(executeStepOutput{Err: "host_exec: " + err.Error()})
	}
	stdout := strings.TrimSpace(out.Stdout)
	stderr := strings.TrimSpace(out.Stderr)
	if out.Error != "" {
		return writeOutput(executeStepOutput{
			ExitCode: out.ExitCode,
			Stdout:   stdout,
			Stderr:   stderr,
			Err:      out.Error,
		})
	}
	success := out.ExitCode == 0
	return writeOutput(executeStepOutput{
		Success:  success,
		Changed:  success && isChangingAction(act),
		ExitCode: out.ExitCode,
		Stdout:   stdout,
		Stderr:   stderr,
	})
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
