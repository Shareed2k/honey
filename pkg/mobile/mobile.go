package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cli"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
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

// Exec runs a shell command on all hosts matching the backends filter.
// requestJSON: {"config_path":"...","backends":"prod-*","command":"uptime","ssh_user":"ubuntu"}
// Returns: {"results":[{"host":"...","output":"...","exit_code":0,"error":"..."}]}
func Exec(requestJSON string) (string, error) {
	var input struct {
		ConfigPath            string `json:"config_path"`
		Backends              string `json:"backends"`
		Name                  string `json:"name"`
		NameRegex             string `json:"name_regex"`
		Providers             string `json:"providers"`
		Host                  string `json:"host"`
		HostIP                string `json:"host_ip"`
		SSHPort               int    `json:"ssh_port"`
		Command               string `json:"command"`
		SSHUser               string `json:"ssh_user"`
		SSHIdentityFile       string `json:"ssh_identity_file"`
		SSHIdentityPassphrase string `json:"ssh_identity_passphrase"`
	}
	if err := json.Unmarshal([]byte(requestJSON), &input); err != nil {
		return "", err
	}

	// When the caller already has the host's IP (from a dashboard record), SSH
	// straight to it and skip the inventory search entirely.
	var recs []hosts.Record
	if ip := strings.TrimSpace(input.HostIP); ip != "" {
		rec := hosts.Record{Name: input.Host, PrimaryIP: ip}
		if input.SSHPort > 0 {
			rec = hosts.CloneWithMetaSSHPort(rec, input.SSHPort)
		}
		recs = []hosts.Record{rec}
	} else {
		searchIn := &hostapi.SearchHostsInput{
			ConfigPath: input.ConfigPath,
			Backends:   input.Backends,
			Name:       input.Name,
			NameRegex:  input.NameRegex,
			Providers:  input.Providers,
		}
		searchOut, err := hostapi.SearchHosts(context.Background(), searchIn, nil, cli.GetSearchRegistry())
		if err != nil {
			return "", err
		}
		if len(searchOut.Records) == 0 {
			b, _ := json.Marshal(map[string]any{"results": []any{}})
			return string(b), nil
		}
		recs = searchOut.Records
	}

	if input.SSHIdentityFile != "" {
		keyPath, cleanup, kerr := materializeKey(input.SSHIdentityFile, input.SSHIdentityPassphrase)
		if kerr != nil {
			return "", fmt.Errorf("ssh key: %w", kerr)
		}
		defer cleanup()
		for i := range recs {
			recs[i] = hosts.CloneWithMetaSSHIdentityFile(recs[i], keyPath)
		}
	}

	sshUser := strings.TrimSpace(input.SSHUser)
	if sshUser == "" {
		sshUser = defaultSSHUser(input.ConfigPath)
	}

	tcs := make([]engine.TargetContext, len(recs))
	for i, r := range recs {
		tcs[i] = engine.TargetContext{Record: r}
	}
	results, err := engine.ExecuteSSHParallel(
		sshUser,
		tcs,
		func(_ hosts.Record) string { return input.Command },
		8,
		cli.GetExecRegistry(),
	)
	if err != nil {
		return "", err
	}

	type item struct {
		Host     string `json:"host"`
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
		Error    string `json:"error,omitempty"`
	}
	items := make([]item, len(results))
	for i, r := range results {
		items[i] = item{Host: r.Name, Output: r.Output, ExitCode: r.ExitCode, Error: r.ErrMsg}
	}
	b, err := json.Marshal(map[string]any{"results": items})
	if err != nil {
		return "", err
	}
	return string(b), nil
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

// GetVersion returns the honey binary version embedded at build time.
func GetVersion() string {
	return cli.BuildVersion()
}
