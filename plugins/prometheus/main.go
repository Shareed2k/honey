//go:build wasip1 || wasm

package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	pdk "github.com/extism/go-pdk"
	extismhttp "github.com/extism/go-pdk/http"
	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/shareed2k/honey/pkg/pluginpdk"
)

//go:wasmimport extism:host/user get_env
func getEnvHost(inputOffset uint64) uint64

// pluginGetEnv reads an operator-allowed env var via the host's get_env
// function. Unlike os.Getenv, this is required inside a wasip1 Extism guest —
// the guest has no direct access to the host process's environment.
func pluginGetEnv(key string) (string, error) {
	mem := pdk.AllocateString(key)
	off := getEnvHost(mem.Offset())
	if off == 0 {
		return "", fmt.Errorf("get_env returned 0 for %s (not in allowed_env?)", key)
	}
	return pdk.ParamString(off), nil
}

// bearerRoundTripper injects an Authorization: Bearer header, if a token is
// set, without mutating the caller's request.
type bearerRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (rt bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.token != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+rt.token)
	}
	return rt.base.RoundTrip(req)
}

type executeStepInput struct {
	Action  string `json:"action"`
	Config  []byte `json:"config,omitempty"`
	Execute bool   `json:"execute"`
}

type executeStepOutput struct {
	Success bool   `json:"success"`
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
	action := strings.TrimSpace(in.Action)
	if err := validateAction(action); err != nil {
		return writeOutput(executeStepOutput{Err: err.Error()})
	}
	cfg, err := pluginpdk.ReadConfig[promConfig](in.Config)
	if err != nil {
		return writeOutput(executeStepOutput{Err: err.Error()})
	}

	promURL, ok := pdk.GetConfig("prometheus_url")
	if !ok || strings.TrimSpace(promURL) == "" {
		return writeOutput(executeStepOutput{Err: "prometheus_url is not configured (set it in plugin.yaml config)"})
	}

	transport := http.RoundTripper(&extismhttp.HTTPTransport{})
	if token, tokErr := pluginGetEnv("PROMETHEUS_BEARER_TOKEN"); tokErr == nil && token != "" {
		transport = bearerRoundTripper{base: transport, token: token}
	}
	client, err := promapi.NewClient(promapi.Config{Address: promURL, RoundTripper: transport})
	if err != nil {
		return writeOutput(executeStepOutput{Err: fmt.Sprintf("build prometheus client: %s", err.Error())})
	}
	v1api := promv1.NewAPI(client)

	// Query is read-only and side-effect-free, so it runs the same way
	// whether this is a dry-run preview or a real execution.
	// A zero time.Time omits the "time" query param, so Prometheus evaluates
	// "now" using its own clock. Do NOT use time.Now() here: verified against
	// a real Prometheus that this WASM guest's clock is not the host's real
	// wall-clock (it read back as a fixed 2022-01-01 under wazero/Extism), so
	// every query would silently ask Prometheus for data as of that fake time
	// and return an empty result.
	out, err := executeQuery(context.Background(), v1api, cfg, time.Time{})
	if err != nil {
		return writeOutput(executeStepOutput{Err: err.Error()})
	}
	return writeOutput(executeStepOutput{Success: true, Stdout: string(out)})
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
