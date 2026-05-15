// Package cuetry parses, validates, and resolves CUE remote recipes for honey.
package cuetry

// Recipe is the decoded "recipe" block from a CUE document.
type Recipe struct {
	Name     string          `json:"name"`
	Defaults *RecipeDefaults `json:"defaults,omitempty"`
	Steps    []RecipeStep    `json:"steps"`
}

// RecipeDefaults holds recipe-level defaults (optional fields).
type RecipeDefaults struct {
	RunAs         string            `json:"run_as,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	K8sDebugImage string            `json:"k8s_debug_image,omitempty"`
	KVTunnel      *bool             `json:"kv_tunnel,omitempty"`
	SSHPort       int               `json:"ssh_port,omitempty"`
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

// RecipeStepHook runs once per target host after that host's main step result is known.
type RecipeStepHook struct {
	Where   string            `json:"where"`
	Command string            `json:"command,omitempty"`
	RunAs   string            `json:"run_as,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Notify  *RecipeNotify     `json:"notify,omitempty"`
}

// RecipeStep is one remote action: exactly one of command, put, get, script, agent_transfer, or ai.
// Host selects targets: literal IP, exact name, "*", "re:…", or "_" for ai only (see resolve.go). For agent_transfer,
// host selects the source endpoint (must match exactly one row); agent_transfer.dest_host selects the destination.
type RecipeStep struct {
	Host          string               `json:"host"`
	SSHPort       int                  `json:"ssh_port,omitempty"`
	Command       string               `json:"command,omitempty"`
	Put           *RecipeFileTransfer  `json:"put,omitempty"`
	Get           *RecipeFileTransfer  `json:"get,omitempty"`
	Script        *RecipeFileTransfer  `json:"script,omitempty"`
	AgentTransfer *RecipeAgentTransfer `json:"agent_transfer,omitempty"`
	AI            *RecipeAI            `json:"ai,omitempty"`
	Notify        *RecipeNotify        `json:"notify,omitempty"`
	Hooks         *RecipeStepHooks     `json:"hooks,omitempty"`
	KVTunnel      *bool                `json:"kv_tunnel,omitempty"`
	RunAs         string               `json:"run_as,omitempty"`
	Env           map[string]string    `json:"env,omitempty"`
}

// NotifyEnabled reports whether the recipe author included a notify block (including notify: {}).
func (s RecipeStep) NotifyEnabled() bool {
	return s.Notify != nil
}
