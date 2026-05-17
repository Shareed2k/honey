// Package v1 defines the honey.plugins/v1 JSON contract between the host and WASM plugins.
package v1

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
	Success bool   `json:"success"`
	Stdout  string `json:"stdout,omitempty"`
	Stderr  string `json:"stderr,omitempty"`
	Err     string `json:"err,omitempty"`
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
