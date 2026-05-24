//go:build wasip1 || wasm

package pluginpdk

import (
	"encoding/json"
)

//go:wasmimport extism:host/user postgres_query
func postgresQueryHost(inputOffset uint64) uint64

//go:wasmimport extism:host/user postgres_exec
func postgresExecHost(inputOffset uint64) uint64

//go:wasmimport extism:host/user postgres_migrate
func postgresMigrateHost(inputOffset uint64) uint64

type PostgresSQLInput struct {
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

type PostgresMigrateInput struct {
	DSNSecret     string            `json:"dsn_secret"`
	MigrationsDir string            `json:"migrations_dir,omitempty"`
	Files         []string          `json:"files,omitempty"`
	TimeoutMS     int               `json:"timeout_ms"`
	Readonly      *bool             `json:"readonly,omitempty"`
	KVKey         string            `json:"kv_key,omitempty"`
	KVKeyPerHost  bool              `json:"kv_key_per_host,omitempty"`
	Extract       map[string]string `json:"extract,omitempty"`
}

type PostgresOutput struct {
	Changed      bool             `json:"changed,omitempty"`
	Failed       bool             `json:"failed,omitempty"`
	Rows         []map[string]any `json:"rows,omitempty"`
	RowsAffected int64            `json:"rows_affected,omitempty"`
	Stdout       string           `json:"stdout,omitempty"`
	Error        string           `json:"error,omitempty"`
}

// PostgresQuery runs a read-only SQL query via the host.
func PostgresQuery(in PostgresSQLInput) (PostgresOutput, error) {
	return callRemote[PostgresOutput](postgresQueryHost, in)
}

// PostgresExec runs a write SQL statement via the host.
func PostgresExec(in PostgresSQLInput) (PostgresOutput, error) {
	return callRemote[PostgresOutput](postgresExecHost, in)
}

// PostgresMigrate applies SQL migration files via the host.
func PostgresMigrate(in PostgresMigrateInput) (PostgresOutput, error) {
	return callRemote[PostgresOutput](postgresMigrateHost, in)
}
