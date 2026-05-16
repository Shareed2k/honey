// Package cuetry parses, validates, and resolves CUE remote recipes for honey.
package cuetry

import "encoding/json"

// Recipe is the decoded "recipe" block from a CUE document.
type Recipe struct {
	Name     string          `json:"name"`
	Type     string          `json:"type,omitempty"`
	Defaults *RecipeDefaults `json:"defaults,omitempty"`
	Steps    []RecipeStep    `json:"steps"`
}

// RecipeDefaults holds recipe-level defaults (optional fields).
type RecipeDefaults struct {
	RunAs         string            `json:"run_as,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Secrets       map[string]string `json:"secrets,omitempty"`
	K8sDebugImage string            `json:"k8s_debug_image,omitempty"`
	KVTunnel      *bool             `json:"kv_tunnel,omitempty"`
	MaxParallel   int               `json:"max_parallel,omitempty"`
	SSHPort       int               `json:"ssh_port,omitempty"`
	SSHPrivateKey string            `json:"ssh_private_key,omitempty"`
}

// RecipeFileTransfer is a local ↔ remote path pair for SFTP put/get steps.
type RecipeFileTransfer struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

// RecipeAgentTransferCloud is the staging object location (S3/GCS, etc.).
type RecipeAgentTransferCloud struct {
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix,omitempty"`
	Object   string `json:"object,omitempty"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// RecipeCloudBackendRef selects a backend entry from honey YAML for signing hints (AWS profile, GCP project).
type RecipeCloudBackendRef struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Index *int   `json:"index,omitempty"`
}

// RecipeAI configures the terminal local LLM summarizer step (must be last in recipe; host must be "_").
type RecipeAI struct {
	Prompt          string `json:"prompt"`
	SystemPrompt    string `json:"system_prompt,omitempty"`
	Model           string `json:"model,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	MaxInputChars   int    `json:"max_input_chars,omitempty"`
}

// RecipeNotifyHTTP marks HTTP default JSON POST URLs (HONEY_NOTIFY_HTTP_URL) as selected in notify.services.
type RecipeNotifyHTTP struct{}

// RecipeNotifySlack marks Slack incoming webhook (HONEY_NOTIFY_SLACK_WEBHOOK_URL); optional channel_id overrides payload channel.
type RecipeNotifySlack struct {
	ChannelID string `json:"channel_id,omitempty"`
}

// RecipeNotifyTelegram marks Telegram (bot token + chat IDs from env).
type RecipeNotifyTelegram struct{}

// RecipeNotifyServices selects notifier backends when non-nil (allowlist). Omitted keys are off for this step.
type RecipeNotifyServices struct {
	HTTP     *RecipeNotifyHTTP     `json:"http,omitempty"`
	Slack    *RecipeNotifySlack    `json:"slack,omitempty"`
	Telegram *RecipeNotifyTelegram `json:"telegram,omitempty"`
}

// RecipeNotify is optional per-step notification (env receivers). A present `notify` object in CUE means enabled, even if empty.
type RecipeNotify struct {
	NotifySubject string                `json:"notify_subject,omitempty"`
	Message       string                `json:"message,omitempty"`
	Services      *RecipeNotifyServices `json:"services,omitempty"`
}

// RecipeAgentTransfer is source host (top-level host) → cloud → destination (dest_host), same flow as the web UI.
type RecipeAgentTransfer struct {
	DestHost        string                    `json:"dest_host"`
	SourcePath      string                    `json:"source_path"`
	DestPath        string                    `json:"dest_path"`
	Cloud           *RecipeAgentTransferCloud `json:"cloud"`
	CloudBackendRef *RecipeCloudBackendRef    `json:"cloud_backend_ref,omitempty"`
	KeepObject      bool                      `json:"keep_object,omitempty"`
	MaxRetries      int                       `json:"max_retries,omitempty"`
	AgentRemoteDir  string                    `json:"agent_remote_dir,omitempty"`
}

// RecipeStepHooks configures optional per-host hooks after the main step outcome (command/script only).
type RecipeStepHooks struct {
	OnSuccess *RecipeStepHook `json:"on_success,omitempty"`
	OnFailure *RecipeStepHook `json:"on_failure,omitempty"`
}

// RecipePluginHook configures a WASM plugin for a local hook (xor with command).
type RecipePluginHook struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Config json.RawMessage `json:"config,omitempty"`
}

// RecipeStepHook runs once per target host after that host's main step result is known.
type RecipeStepHook struct {
	Where   string            `json:"where"`
	Command string            `json:"command,omitempty"`
	Plugin  *RecipePluginHook `json:"plugin,omitempty"`
	RunAs   string            `json:"run_as,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty"`
	Notify  *RecipeNotify     `json:"notify,omitempty"`
}

// RecipeStepPlugin configures a WASM custom_step plugin action.
type RecipeStepPlugin struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Config json.RawMessage `json:"config,omitempty"`
}

// RecipeStep is one remote action: exactly one of command, put, get, script, agent_transfer, ai, or plugin.
// Host selects targets: literal IP, exact name, "*", "re:…", or "_" for ai only (see resolve.go). For agent_transfer,
// host selects the source endpoint (must match exactly one row); agent_transfer.dest_host selects the destination.
type RecipeStep struct {
	ID            string               `json:"id,omitempty"`
	Depends       []string             `json:"depends,omitempty"`
	Host          string               `json:"host"`
	SSHPort       int                  `json:"ssh_port,omitempty"`
	SSHPrivateKey string               `json:"ssh_private_key,omitempty"`
	Command       string               `json:"command,omitempty"`
	Put           *RecipeFileTransfer  `json:"put,omitempty"`
	Get           *RecipeFileTransfer  `json:"get,omitempty"`
	Script        *RecipeFileTransfer  `json:"script,omitempty"`
	AgentTransfer *RecipeAgentTransfer `json:"agent_transfer,omitempty"`
	AI            *RecipeAI            `json:"ai,omitempty"`
	Plugin        *RecipeStepPlugin    `json:"plugin,omitempty"`
	Notify        *RecipeNotify        `json:"notify,omitempty"`
	Hooks         *RecipeStepHooks     `json:"hooks,omitempty"`
	KVTunnel      *bool                `json:"kv_tunnel,omitempty"`
	MaxParallel   int                  `json:"max_parallel,omitempty"`
	EnvFrom       []EnvFromRef         `json:"env_from,omitempty"`
	RunAs         string               `json:"run_as,omitempty"`
	Env           map[string]string    `json:"env,omitempty"`
	Secrets       map[string]string    `json:"secrets,omitempty"`
}

// NotifyEnabled reports whether the recipe author included a notify block (including notify: {}).
func (s RecipeStep) NotifyEnabled() bool {
	return s.Notify != nil
}
