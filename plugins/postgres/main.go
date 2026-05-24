//go:build wasip1 || wasm

package main

import (
	"encoding/json"
	"strings"

	"github.com/extism/go-pdk"
	"github.com/shareed2k/honey/pkg/pluginpdk"
)

type queryConfig struct {
	DSNSecret    string            `json:"dsn_secret"`
	SQL          string            `json:"sql"`
	Params       json.RawMessage   `json:"params,omitempty"`
	TimeoutMS    int               `json:"timeout_ms"`
	Readonly     *bool             `json:"readonly,omitempty"`
	KVKey        string            `json:"kv_key,omitempty"`
	KVKeyPerHost bool              `json:"kv_key_per_host,omitempty"`
	Extract      map[string]string `json:"extract,omitempty"`
	Host         string            `json:"host,omitempty"`
	Port         string            `json:"port,omitempty"`
	TunnelStep   string            `json:"tunnel_step,omitempty"`
}

type migrateConfig struct {
	DSNSecret     string            `json:"dsn_secret"`
	MigrationsDir string            `json:"migrations_dir,omitempty"`
	Files         []string          `json:"files,omitempty"`
	TimeoutMS     int               `json:"timeout_ms"`
	Readonly      *bool             `json:"readonly,omitempty"`
	KVKey         string            `json:"kv_key,omitempty"`
	KVKeyPerHost  bool              `json:"kv_key_per_host,omitempty"`
	Extract       map[string]string `json:"extract,omitempty"`
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
	action := strings.TrimSpace(in.Action)
	if !in.Execute {
		return writeOutput(executeStepOutput{
			Success: true,
			Changed: true,
			Stdout:  "would run postgres " + action,
		})
	}
	switch action {
	case "query":
		return runQuery(in.Config)
	case "exec":
		return runExec(in.Config)
	case "migrate":
		return runMigrate(in.Config)
	default:
		return writeOutput(executeStepOutput{Success: false, Err: "unknown action " + action})
	}
}

func runQuery(config []byte) int32 {
	cfg, err := pluginpdk.ReadConfig[queryConfig](config)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	out, err := pluginpdk.PostgresQuery(pluginpdk.PostgresSQLInput{
		DSNSecret:    cfg.DSNSecret,
		SQL:          cfg.SQL,
		Params:       cfg.Params,
		TimeoutMS:    cfg.TimeoutMS,
		Readonly:     cfg.Readonly,
		KVKey:        cfg.KVKey,
		KVKeyPerHost: cfg.KVKeyPerHost,
		Extract:      cfg.Extract,
		Host:         cfg.Host,
		Port:         cfg.Port,
		TunnelStep:   cfg.TunnelStep,
	})
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	return postgresOutput(out)
}

func runExec(config []byte) int32 {
	cfg, err := pluginpdk.ReadConfig[queryConfig](config)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	out, err := pluginpdk.PostgresExec(pluginpdk.PostgresSQLInput{
		DSNSecret:    cfg.DSNSecret,
		SQL:          cfg.SQL,
		Params:       cfg.Params,
		TimeoutMS:    cfg.TimeoutMS,
		Readonly:     cfg.Readonly,
		KVKey:        cfg.KVKey,
		KVKeyPerHost: cfg.KVKeyPerHost,
		Extract:      cfg.Extract,
		Host:         cfg.Host,
		Port:         cfg.Port,
		TunnelStep:   cfg.TunnelStep,
	})
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	return postgresOutput(out)
}

func runMigrate(config []byte) int32 {
	cfg, err := pluginpdk.ReadConfig[migrateConfig](config)
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	out, err := pluginpdk.PostgresMigrate(pluginpdk.PostgresMigrateInput{
		DSNSecret:     cfg.DSNSecret,
		MigrationsDir: cfg.MigrationsDir,
		Files:         cfg.Files,
		TimeoutMS:     cfg.TimeoutMS,
		Readonly:      cfg.Readonly,
		KVKey:         cfg.KVKey,
		KVKeyPerHost:  cfg.KVKeyPerHost,
		Extract:       cfg.Extract,
	})
	if err != nil {
		return writeOutput(executeStepOutput{Success: false, Err: err.Error()})
	}
	return postgresOutput(out)
}

func postgresOutput(out pluginpdk.PostgresOutput) int32 {
	if out.Error != "" || out.Failed {
		return writeOutput(executeStepOutput{Success: false, Changed: out.Changed, Err: out.Error, Stdout: out.Stdout})
	}
	stdout := strings.TrimSpace(out.Stdout)
	if stdout == "" && len(out.Rows) > 0 {
		b, _ := json.Marshal(out.Rows)
		stdout = string(b)
	}
	return writeOutput(executeStepOutput{Success: true, Changed: out.Changed, Stdout: stdout})
}

func writeOutput(out executeStepOutput) int32 {
	if err := pdk.OutputJSON(out); err != nil {
		pdk.SetError(err)
		return 1
	}
	return 0
}
