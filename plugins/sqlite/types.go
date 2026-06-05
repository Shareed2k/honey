package main

import "encoding/json"

type sqliteConfig struct {
	DSN          string          `json:"dsn"`
	SQL          string          `json:"sql"`
	Params       []any           `json:"params,omitempty"`
	TimeoutMS    int             `json:"timeout_ms,omitempty"`
	Readonly     *bool           `json:"readonly,omitempty"`
	KVKey        string          `json:"kv_key,omitempty"`
	KVKeyPerHost bool            `json:"kv_key_per_host,omitempty"`
	Extract      json.RawMessage `json:"extract,omitempty"`
}

type executeStepInput struct {
	Action  string `json:"action"`
	Config  []byte `json:"config,omitempty"`
	Host    []byte `json:"host,omitempty"`
	Execute bool   `json:"execute"`
}

type executeStepOutput struct {
	Success bool   `json:"success"`
	Changed bool   `json:"changed,omitempty"`
	Stdout  string `json:"stdout,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
	Err     string `json:"err,omitempty"`
}
