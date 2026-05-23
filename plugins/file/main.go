//go:build wasip1 || wasm

package main

import (
	"fmt"
	"strings"

	"github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
)

type fileConfig struct {
	Path  string `json:"path"`
	State string `json:"state"`
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
	Stderr  string `json:"stderr,omitempty"`
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
	if strings.TrimSpace(in.Action) != "manage" {
		return writeOutput(executeStepOutput{Success: false, Err: "unknown action " + in.Action})
	}
	cfg, err := pluginpdk.ReadConfig[fileConfig](in.Config)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	path := strings.TrimSpace(cfg.Path)
	state := strings.ToLower(strings.TrimSpace(cfg.State))
	if path == "" || state == "" {
		return writeOutput(executeStepOutput{Success: false, Err: "config.path and config.state are required"})
	}
	if !in.Execute {
		return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: fmt.Sprintf("would set file %s state=%s", path, state)})
	}
	switch state {
	case "directory":
		return ensureDirectory(path)
	case "absent":
		return ensureAbsent(path)
	case "touch":
		return ensureTouch(path)
	default:
		return writeOutput(executeStepOutput{Success: false, Err: "unsupported state " + state})
	}
}

func ensureDirectory(path string) int32 {
	stat, err := pluginpdk.RemoteStat(path)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	if stat.Error != "" {
		return writeOutput(executeStepOutput{Success: false, Err: stat.Error})
	}
	if stat.Exists && stat.IsDir {
		return writeOutput(executeStepOutput{Success: true, Changed: false, Stdout: "directory already exists"})
	}
	script := fmt.Sprintf("mkdir -p %s", shellQuote(path))
	out, err := pluginpdk.RemoteExec("/bin/sh", script)
	if err != nil || out.Error != "" || out.Failed {
		return writeOutput(executeStepOutput{Success: false, Err: firstErr(err, out.Error), Stdout: out.Stdout, Stderr: out.Stderr})
	}
	return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: "created directory"})
}

func ensureAbsent(path string) int32 {
	stat, err := pluginpdk.RemoteStat(path)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	if stat.Error != "" {
		return writeOutput(executeStepOutput{Success: false, Err: stat.Error})
	}
	if !stat.Exists {
		return writeOutput(executeStepOutput{Success: true, Changed: false, Stdout: "already absent"})
	}
	script := fmt.Sprintf("rm -rf %s", shellQuote(path))
	out, err := pluginpdk.RemoteExec("/bin/sh", script)
	if err != nil || out.Error != "" || out.Failed {
		return writeOutput(executeStepOutput{Success: false, Err: firstErr(err, out.Error), Stdout: out.Stdout, Stderr: out.Stderr})
	}
	return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: "removed"})
}

func ensureTouch(path string) int32 {
	stat, err := pluginpdk.RemoteStat(path)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	if stat.Error != "" {
		return writeOutput(executeStepOutput{Success: false, Err: stat.Error})
	}
	if stat.Exists && !stat.IsDir {
		return writeOutput(executeStepOutput{Success: true, Changed: false, Stdout: "file already exists"})
	}
	script := fmt.Sprintf("mkdir -p $(dirname %s) && : > %s", shellQuote(path), shellQuote(path))
	out, err := pluginpdk.RemoteExec("/bin/sh", script)
	if err != nil || out.Error != "" || out.Failed {
		return writeOutput(executeStepOutput{Success: false, Err: firstErr(err, out.Error), Stdout: out.Stdout, Stderr: out.Stderr})
	}
	return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: "touched file"})
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func firstErr(err error, msg string) string {
	if err != nil {
		return err.Error()
	}
	return msg
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
