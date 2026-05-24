//go:build wasip1 || wasm

package main

import (
	"strings"

	"github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
)

type putConfig struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
	Mode   string `json:"mode,omitempty"`
}

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

func main() {}

//go:wasmexport execute_step
func executeStep() int32 {
	var in executeStepInput
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	if strings.TrimSpace(in.Action) != "put" {
		return writeOutput(executeStepOutput{Success: false, Err: "unknown action " + in.Action})
	}
	cfg, err := pluginpdk.ReadConfig[putConfig](in.Config)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	if strings.TrimSpace(cfg.Local) == "" || strings.TrimSpace(cfg.Remote) == "" {
		return writeOutput(executeStepOutput{Success: false, Err: "config.local and config.remote are required"})
	}
	if !in.Execute {
		return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: "would copy " + cfg.Local + " -> " + cfg.Remote})
	}
	out, err := pluginpdk.RemoteUpload(cfg.Local, cfg.Remote, cfg.Mode)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	if out.Error != "" || out.Failed {
		return writeOutput(executeStepOutput{Success: false, Changed: out.Changed, Err: out.Error})
	}
	return writeOutput(executeStepOutput{Success: true, Changed: out.Changed, Stdout: "copied " + cfg.Remote})
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
