//go:build wasip1 || wasm

// Package main implements the k8s-probe example plugin.
// It demonstrates pluginpdk.K8sHTTP — calling the Kubernetes API from
// a WASM plugin without any credentials or TCP sockets.
// honey routes the call through the k8s_http host function which uses
// the kubeconfig stored in the host record's metadata.
//
// Actions:
//   version  — GET /version → cluster version JSON
//   health   — GET /readyz  → cluster health status
//   nodes    — GET /api/v1/nodes → node list JSON
//
// Example CUE recipe:
//
//	plugin: {
//	  id:     "k8s-probe"
//	  action: "version"
//	  config: {}
//	}
package main

import (
	"fmt"
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

	action := strings.TrimSpace(in.Action)

	if !in.Execute {
		return writeOutput(executeStepOutput{
			Success: true,
			Stdout:  fmt.Sprintf("(dry-run) would probe k8s action=%q", action),
		})
	}

	var path string
	switch action {
	case "version":
		path = "/version"
	case "health":
		path = "/readyz"
	case "nodes":
		path = "/api/v1/nodes"
	default:
		return writeOutput(executeStepOutput{
			Err: fmt.Sprintf("unknown action %q (want: version, health, nodes)", action),
		})
	}

	// Call the Kubernetes API via the k8s_http host function.
	// honey resolves the API server URL and credentials automatically
	// from the host record — the plugin does not need kubeconfig.
	out, err := pluginpdk.K8sHTTP("GET", path, nil, nil)
	if err != nil {
		return writeOutput(executeStepOutput{Err: "k8s_http: " + err.Error()})
	}
	if out.Error != "" {
		return writeOutput(executeStepOutput{Err: out.Error})
	}
	if out.StatusCode >= 400 {
		return writeOutput(executeStepOutput{
			Err:    fmt.Sprintf("k8s API returned HTTP %d", out.StatusCode),
			Stdout: string(out.Body),
		})
	}
	return writeOutput(executeStepOutput{
		Success: true,
		Stdout:  string(out.Body),
	})
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
