package mobile

import (
	"context"
	"encoding/json"

	"github.com/shareed2k/honey/internal/cli"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
)

// SearchHosts takes a JSON serialized hostapi.SearchHostsInput and returns a JSON serialized hostapi.SearchHostsOutput.
func SearchHosts(requestJSON string) (string, error) {
	var input hostapi.SearchHostsInput
	if err := json.Unmarshal([]byte(requestJSON), &input); err != nil {
		return "", err
	}

	reg := cli.GetSearchRegistry()

	out, err := hostapi.SearchHosts(context.Background(), &input, nil, reg)
	if err != nil {
		return "", err
	}

	resp, err := json.Marshal(out)
	if err != nil {
		return "", err
	}

	return string(resp), nil
}

// ListBackends takes a JSON serialized config path request and returns a JSON serialized hostapi.ListBackendsOutput.
func ListBackends(requestJSON string) (string, error) {
	var input struct {
		ConfigPath string `json:"config_path"`
	}
	if err := json.Unmarshal([]byte(requestJSON), &input); err != nil {
		return "", err
	}

	reg := cli.GetSearchRegistry()

	out, err := hostapi.ListBackends(input.ConfigPath, reg)
	if err != nil {
		empty, _ := json.Marshal(hostapi.ListBackendsOutput{Backends: []config.BackendRow{}})
		return string(empty), nil
	}

	resp, err := json.Marshal(out)
	if err != nil {
		return "", err
	}

	return string(resp), nil
}

// ExecuteRecipe is the gomobile entrypoint.
func ExecuteRecipe(requestJSON string, cb LogCallback) (string, error) {
	_ = requestJSON // unused for now

	if cb != nil {
		cb.OnLog("Initializing honey engine...")
	}

	// Core engine integration will go here.
	// For now, return a dummy successful response.
	return `{"status": "success"}`, nil
}
