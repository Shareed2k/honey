// Package v1 defines the honey.plugins/v1 JSON contract between the host and WASM plugins.
package v1

import "encoding/json"

// APIVersion is the JSON api_version field for honey.plugins/v1.
const APIVersion = "honey.plugins/v1"

// CueTransformInput is passed to the cue_transform export.
type CueTransformInput struct {
	APIVersion string `json:"api_version"`
	Cue        string `json:"cue"` // base64-encoded CUE bytes
	HostsCount int    `json:"hosts_count"`
}

// CueTransformOutput is returned from cue_transform.
type CueTransformOutput struct {
	Cue string `json:"cue"` // base64-encoded CUE bytes
}

// ExecuteStepInput is passed to execute_step.
type ExecuteStepInput struct {
	APIVersion string            `json:"api_version"`
	StepIndex  int               `json:"step_index"`
	Host       []byte            `json:"host"` // JSON host record
	Env        map[string]string `json:"env,omitempty"`
	PluginID   string            `json:"plugin_id"`
	Action     string            `json:"action"`
	Config     []byte            `json:"config,omitempty"` // JSON object
	Execute    bool              `json:"execute"`
	SecretsDry bool              `json:"secrets_dry_run"`
}

// ExecuteStepOutput is returned from execute_step.
type ExecuteStepOutput struct {
	Success  bool   `json:"success"`
	Changed  bool   `json:"changed,omitempty"`
	Skipped  bool   `json:"skipped,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Err      string `json:"err,omitempty"`
}

// ResolveSecretInput is passed to resolve_secret.
type ResolveSecretInput struct {
	APIVersion string `json:"api_version"`
	Ref        string `json:"ref"`
	Label      string `json:"label,omitempty"`
	PluginID   string `json:"plugin_id"`
}

// ResolveSecretOutput is returned from resolve_secret.
type ResolveSecretOutput struct {
	Value string `json:"value"`
}

// UnwrapStackKeyInput is passed to unwrap_stack_key.
type UnwrapStackKeyInput struct {
	APIVersion      string `json:"api_version"`
	SecretsProvider string `json:"secrets_provider"`
	EncryptedKey    string `json:"encrypted_key"`
	PluginID        string `json:"plugin_id"`
}

// UnwrapStackKeyOutput is returned from unwrap_stack_key (hex-encoded 32-byte key).
type UnwrapStackKeyOutput struct {
	DataKeyHex string `json:"data_key_hex"`
}

// OnStepResultInput is passed to on_step_result (local hooks).
type OnStepResultInput struct {
	APIVersion string            `json:"api_version"`
	RecipeName string            `json:"recipe_name"`
	StepIndex  int               `json:"step_index"`
	Phase      string            `json:"phase"`
	Host       []byte            `json:"host"`
	Result     []byte            `json:"result"` // JSON HostExecResult-like map
	PluginID   string            `json:"plugin_id"`
	Action     string            `json:"action"`
	Config     []byte            `json:"config,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

// OnStepResultOutput is returned from on_step_result.
type OnStepResultOutput struct {
	Output string `json:"output,omitempty"`
	Err    string `json:"err,omitempty"`
}

// PluginError is returned when a plugin sets an error string (non-empty means failure).
type PluginError struct {
	Error string `json:"error"`
}

// HostExecInput is passed to the host_exec host function (argv-only, no shell).
type HostExecInput struct {
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd,omitempty"`
	TimeoutMS int      `json:"timeout_ms,omitempty"`
}

// HostExecOutput is returned from host_exec.
type HostExecOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"`
}

// KVInput is passed to the kv host function.
type KVInput struct {
	Op    string `json:"op"` // get, put, delete
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

// KVOutput is returned from kv.
type KVOutput struct {
	Found bool   `json:"found,omitempty"`
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// RemoteExecInput is passed to the remote_exec host function.
type RemoteExecInput struct {
	Shell     string `json:"shell,omitempty"`
	Script    string `json:"script,omitempty"`
	RunAs     string `json:"run_as,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// RemoteExecOutput is returned from remote_exec.
type RemoteExecOutput struct {
	ExitCode int    `json:"exit_code,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Changed  bool   `json:"changed,omitempty"`
	Failed   bool   `json:"failed,omitempty"`
	Error    string `json:"error,omitempty"`
}

// RemoteUploadInput is passed to the remote_upload host function.
type RemoteUploadInput struct {
	LocalPath  string `json:"local_path,omitempty"`
	RemotePath string `json:"remote_path"`
	Mode       string `json:"mode,omitempty"`
	Content    string `json:"content,omitempty"`
}

// RemoteUploadOutput is returned from remote_upload.
type RemoteUploadOutput struct {
	Changed bool   `json:"changed,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RemoteDownloadInput is passed to the remote_download host function.
type RemoteDownloadInput struct {
	RemotePath string `json:"remote_path"`
	LocalPath  string `json:"local_path,omitempty"`
	MaxBytes   int64  `json:"max_bytes,omitempty"`
}

// RemoteDownloadOutput is returned from remote_download.
type RemoteDownloadOutput struct {
	Content string `json:"content,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RemoteStatInput is passed to the remote_stat host function.
type RemoteStatInput struct {
	Path string `json:"path"`
}

// RemoteStatOutput is returned from remote_stat.
type RemoteStatOutput struct {
	Exists  bool   `json:"exists,omitempty"`
	IsDir   bool   `json:"is_dir,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Size    int64  `json:"size,omitempty"`
	MTime   string `json:"mtime,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}

// TemplateRenderInput is passed to the template_render host function.
type TemplateRenderInput struct {
	Template string         `json:"template"`
	Data     map[string]any `json:"data,omitempty"`
}

// TemplateRenderOutput is returned from template_render.
type TemplateRenderOutput struct {
	Content string `json:"content,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}

// PostgresSQLInput is shared by postgres_query and postgres_exec host functions.
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

// PostgresMigrateInput is passed to the postgres_migrate host function.
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

// PostgresOutput is returned from postgres_query, postgres_exec, and postgres_migrate.
type PostgresOutput struct {
	Changed      bool             `json:"changed,omitempty"`
	Failed       bool             `json:"failed,omitempty"`
	Rows         []map[string]any `json:"rows,omitempty"`
	RowsAffected int64            `json:"rows_affected,omitempty"`
	Stdout       string           `json:"stdout,omitempty"`
	Error        string           `json:"error,omitempty"`
}

// ExecRequest is the shared contract a docker-runtime plugin's dockerTransport
// (internal/plugins/docker_transport.go) POSTs to honey-plugin-init: a
// fully-resolved argv (already mapped from the step's config via plugin.cue
// on the honey-process side — honey-plugin-init has no knowledge of CUE,
// config, or plugin manifests, only this shape).
//
// Env carries this call's resolved per-action env (plugin.cue's action.env,
// itself evaluated from the recipe step's config/secrets) — set only on the
// exec'd child process's environment, not the container's own. This is how a
// per-recipe secret (e.g. a DB password) reaches the process without
// appearing in Argv, where it would be visible via `ps`/`/proc/<pid>/cmdline`
// inside the container. Distinct from the manifest's static allowed_env
// (Task 7's resolveAllowedEnv), which is fixed at container-create time from
// honey's own process environment and can't carry a per-call value.
type ExecRequest struct {
	Argv  []string          `json:"argv"`
	Env   map[string]string `json:"env,omitempty"`
	Stdin []byte            `json:"stdin,omitempty"`
}

// ExecResponse reports what happened running an ExecRequest's argv. Error is
// set only for shim-level failures (empty argv, exec.LookPath failure); a
// nonzero ExitCode from a binary that ran fine is NOT an Error — the caller
// (dockerTransport) decides what a nonzero exit means for that action.
type ExecResponse struct {
	Output   string `json:"output,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}
