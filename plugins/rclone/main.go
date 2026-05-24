//go:build wasip1 || wasm

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
)

const rcloneSecretPrefix = "rclone:"

//go:wasmimport extism:host/user get_env
func getEnvHost(inputOffset uint64) uint64

type rcloneConfig struct {
	TunnelStep string         `json:"tunnel_step"`
	BaseURL    string         `json:"base_url"`
	RCUser     string         `json:"rc_user"`
	RCPass     string         `json:"rc_pass"`
	RCPassRef  string         `json:"rc_pass_ref"`
	Params     map[string]any `json:"params"`
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

type resolveSecretInput struct {
	Ref string `json:"ref"`
}

type resolveSecretOutput struct {
	Value string `json:"value"`
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
	if action == "" {
		return writeOutput(executeStepOutput{Success: false, Err: "action is required"})
	}
	cfg, err := pluginpdk.ReadConfig[rcloneConfig](in.Config)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if !in.Execute {
		if baseURL == "" {
			return writeOutput(executeStepOutput{Success: true, Changed: false, Stdout: fmt.Sprintf("would run rclone %s via tunneled rcd (start tunnel step first on execute)", action)})
		}
		body, status, err := rcPost(baseURL, cfg.RCUser, cfg.RCPass, "core/noop", nil)
		if err != nil {
			return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
		}
		if status < 200 || status >= 300 {
			return writeOutput(executeStepOutput{Success: false, Err: fmt.Sprintf("rc noop HTTP %d: %s", status, truncate(string(body), 512))})
		}
		return writeOutput(executeStepOutput{Success: true, Changed: false, Stdout: fmt.Sprintf("dry-run ok (rcd %s); would run %s", baseURL, action)})
	}
	if baseURL == "" {
		return writeOutput(executeStepOutput{Success: false, Err: "base_url is required (set tunnel_step on recipe plugin config)"})
	}
	path, err := rcActionPath(action)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	params := cfg.Params
	if params == nil {
		params = map[string]any{}
	}
	body, status, err := rcPost(baseURL, cfg.RCUser, cfg.RCPass, path, params)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	stdout := strings.TrimSpace(string(body))
	if status < 200 || status >= 300 {
		if stdout == "" {
			stdout = fmt.Sprintf("HTTP %d", status)
		}
		return writeOutput(executeStepOutput{Success: false, Err: stdout})
	}
	return writeOutput(executeStepOutput{Success: true, Changed: true, Stdout: stdout})
}

//go:wasmexport resolve_secret
func resolveSecret() int32 {
	var in resolveSecretInput
	if err := pdk.InputJSON(&in); err != nil {
		pdk.SetError(err)
		return 1
	}
	ref := strings.TrimSpace(in.Ref)
	if !strings.HasPrefix(ref, rcloneSecretPrefix) {
		pdk.SetErrorString("ref missing rclone: prefix")
		return 1
	}
	name := strings.TrimSpace(strings.TrimPrefix(ref, rcloneSecretPrefix))
	if name == "" {
		pdk.SetErrorString("empty rclone secret name")
		return 1
	}
	envKey := "RCLONE_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	val, err := pluginGetEnv(envKey)
	if err != nil {
		pdk.SetErrorString(err.Error())
		return 1
	}
	if strings.TrimSpace(val) == "" {
		pdk.SetErrorString("empty value for " + envKey + " (set operator env or use recipe secrets with rc_pass)")
		return 1
	}
	if err := pdk.OutputJSON(resolveSecretOutput{Value: val}); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}

func pluginGetEnv(key string) (string, error) {
	mem := pdk.AllocateString(key)
	off := getEnvHost(mem.Offset())
	if off == 0 {
		return "", fmt.Errorf("get_env returned 0 for %s (not in allowed_env?)", key)
	}
	return pdk.ParamString(off), nil
}

func rcPost(baseURL, user, pass, path string, params map[string]any) ([]byte, int, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	path = strings.TrimPrefix(strings.TrimSpace(path), "/")
	url := baseURL + "/" + path
	var body []byte
	if len(params) > 0 {
		var err error
		body, err = json.Marshal(params)
		if err != nil {
			return nil, 0, err
		}
	} else {
		body = []byte("{}")
	}
	req := pdk.NewHTTPRequest(pdk.MethodPost, url)
	req.SetHeader("Content-Type", "application/json")
	if auth := basicAuth(user, pass); auth != "" {
		req.SetHeader("Authorization", auth)
	}
	req.SetBody(body)
	resp := req.Send()
	return resp.Body(), int(resp.Status()), nil
}

func basicAuth(user, pass string) string {
	user = strings.TrimSpace(user)
	pass = strings.TrimSpace(pass)
	if user == "" && pass == "" {
		return ""
	}
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return "Basic " + token
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
