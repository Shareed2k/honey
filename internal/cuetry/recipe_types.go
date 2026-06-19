// Package cuetry parses, validates, and resolves CUE remote recipes for honey.
package cuetry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets"
)

// Recipe is the decoded "recipe" block from a CUE document.
type Recipe struct {
	Name      string                    `json:"name"`
	Type      string                    `json:"type,omitempty"`
	Webhooks  map[string]RecipeWebhook  `json:"webhooks,omitempty"`
	Schedules map[string]RecipeSchedule `json:"schedules,omitempty"`
	Defaults  *RecipeDefaults           `json:"defaults,omitempty"`
	Steps     []StepWrapper             `json:"steps"`
	Handlers  []StepWrapper             `json:"handlers,omitempty"`
}

// RecipeSchedule defines a cron-based trigger for a recipe.
type RecipeSchedule struct {
	Cron     string            `json:"cron"`               // 5-field cron expression, e.g. "0 2 * * *"
	TimeZone string            `json:"timezone,omitempty"` // IANA tz name, e.g. "America/New_York"; defaults to UTC
	Env      map[string]string `json:"env,omitempty"`      // static env vars injected at execution time
}

// RecipeWebhook defines a webhook trigger for a recipe.
type RecipeWebhook struct {
	AuthSecret string            `json:"auth_secret,omitempty"`
	Extract    map[string]string `json:"extract,omitempty"`
	Async      *bool             `json:"async,omitempty"`
}

// RecipePrompt defines metadata for UI form generation.
type RecipePrompt struct {
	Description     string   `json:"description,omitempty"`
	Type            string   `json:"type,omitempty"`
	Required        bool     `json:"required,omitempty"`
	Choices         []string `json:"choices,omitempty"`
	ChoicesURL      string   `json:"choices_url,omitempty"`
	ChoicesJSONPath string   `json:"choices_json_path,omitempty"`
	Default         string   `json:"default,omitempty"`
	Multi           bool     `json:"multi,omitempty"`
	Regex           string   `json:"regex,omitempty"`
}

// RecipeDefaults holds recipe-level defaults (optional fields).
type RecipeDefaults struct {
	RunAs         string                  `json:"run_as,omitempty"`
	Env           map[string]string       `json:"env,omitempty"`
	Secrets       map[string]string       `json:"secrets,omitempty"`
	Prompts       map[string]RecipePrompt `json:"prompts,omitempty"`
	K8sDebugImage string                  `json:"k8s_debug_image,omitempty"`
	KVTunnel      *bool                   `json:"kv_tunnel,omitempty"`
	MaxParallel   int                     `json:"max_parallel,omitempty"`
	SSHPort       int                     `json:"ssh_port,omitempty"`
	SSHPrivateKey string                  `json:"ssh_private_key,omitempty"`
	Retry         *RecipeStepRetry        `json:"retry,omitempty"`
	GatherFacts   *bool                   `json:"gather_facts,omitempty"`
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

// RecipeStepTemplate configures a local Go text/template render step (host must be "_").
type RecipeStepTemplate struct {
	Template string         `json:"template"`
	Data     map[string]any `json:"data,omitempty"`
	Output   string         `json:"output,omitempty"`
}

// RecipeAI configures the terminal local LLM summarizer step (must be last in recipe; host must be "_").
type RecipeAI struct {
	Prompt          string `json:"prompt"`
	SystemPrompt    string `json:"system_prompt,omitempty"`
	Model           string `json:"model,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
	MaxInputChars   int    `json:"max_input_chars,omitempty"`
}

// ResolveSystemPrompt returns the system message for a recipe ai step.
// Precedence: non-empty ai.system_prompt in CUE, then config defaults.ai_system_prompt, then built-in default.
func (ai *RecipeAI) ResolveSystemPrompt(configDefault string) string {
	if ai != nil && strings.TrimSpace(ai.SystemPrompt) != "" {
		return strings.TrimSpace(ai.SystemPrompt)
	}
	if strings.TrimSpace(configDefault) != "" {
		return strings.TrimSpace(configDefault)
	}
	return DefaultRecipeAISystemPrompt
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

// EffectiveHookWhere returns the hook execution location. Empty defaults to remote.
func EffectiveHookWhere(hook *RecipeStepHook) string {
	if hook == nil {
		return "remote"
	}
	where := strings.TrimSpace(hook.Where)
	if where == "" {
		return "remote"
	}
	return where
}

// StepOutputName returns the step-level or legacy nested capture name.
func StepOutputName(s Step) string {
	if out := strings.TrimSpace(s.Base().Output); out != "" {
		return out
	}
	if ts, ok := s.(*TemplateStep); ok && ts.Template != nil {
		return strings.TrimSpace(ts.Template.Output)
	}
	return ""
}

// RecipeStepPlugin configures a WASM custom_step plugin action.
type RecipeStepPlugin struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Config json.RawMessage `json:"config,omitempty"`
}

// RecipeLoop configures dynamic runtime fan-out based on a previous step's captured output.
type RecipeLoop struct {
	Step    string `json:"step"`
	Extract string `json:"extract"` // jq expression to extract a JSON array
}

// RecipeStepK8s configures a Kubernetes API step.
// Exactly one action field (Apply/Delete/Scale/RolloutRestart/Wait/Get/Exec/CreateJob) must be set.
// Output, when non-empty, stores the action result in RecipeOutputCapture for downstream env_from.
type RecipeStepK8s struct {
	Namespace      string             `json:"namespace,omitempty"`
	Output         string             `json:"output,omitempty"`
	Apply          *K8sApply          `json:"apply,omitempty"`
	Delete         *K8sDelete         `json:"delete,omitempty"`
	Scale          *K8sScale          `json:"scale,omitempty"`
	RolloutRestart *K8sRolloutRestart `json:"rollout_restart,omitempty"`
	Wait           *K8sWait           `json:"wait,omitempty"`
	Get            *K8sGet            `json:"get,omitempty"`
	Exec           *K8sExec           `json:"exec,omitempty"`
	CreateJob      *K8sCreateJob      `json:"create_job,omitempty"`
}

// K8sApply applies a YAML/JSON manifest via server-side apply.
type K8sApply struct {
	Manifest   string `json:"manifest"`
	Force      bool   `json:"force,omitempty"`
	ServerSide bool   `json:"server_side,omitempty"`
}

// K8sDelete deletes a resource by kind/name (e.g. "deployment/app").
type K8sDelete struct {
	Resource string `json:"resource"`
	Wait     bool   `json:"wait,omitempty"`
}

// K8sScale sets replica count on a scalable resource (e.g. "deployment/app").
type K8sScale struct {
	Resource string `json:"resource"`
	Replicas int32  `json:"replicas"`
}

// K8sRolloutRestart triggers a rolling restart by patching the restart annotation.
type K8sRolloutRestart struct {
	Resource string `json:"resource"`
	Wait     bool   `json:"wait,omitempty"`
}

// K8sWait polls a resource until a condition is met (e.g. "condition=available").
type K8sWait struct {
	Resource string `json:"resource"`
	For      string `json:"for"`
	Timeout  string `json:"timeout,omitempty"`
}

// K8sGet fetches a resource and writes JSON/YAML to stdout.
type K8sGet struct {
	Resource      string `json:"resource"`
	LabelSelector string `json:"label_selector,omitempty"`
	Format        string `json:"format,omitempty"`
}

// K8sExec runs a command in an existing pod container via the exec subresource.
type K8sExec struct {
	Pod       string   `json:"pod"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command"`
	TTY       bool     `json:"tty,omitempty"`
}

// K8sCreateJob creates a batch job and optionally waits for completion.
type K8sCreateJob struct {
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Command       []string          `json:"command,omitempty"`
	Args          []string          `json:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	RestartPolicy string            `json:"restart_policy,omitempty"`
	Wait          bool              `json:"wait,omitempty"`
	TTLSeconds    int32             `json:"ttl_seconds,omitempty"`
}

// RecipeStepDocker configures a Docker engine API step.
type RecipeStepDocker struct {
	Action string       `json:"action"`
	Output string       `json:"output,omitempty"`
	Build  *DockerBuild `json:"build,omitempty"`
	Push   *DockerPush  `json:"push,omitempty"`
	Pull   *DockerPull  `json:"pull,omitempty"`
	Run    *DockerRun   `json:"run,omitempty"`
	Exec   *DockerExec  `json:"exec,omitempty"`
	Stop   *DockerStop  `json:"stop,omitempty"`
}

// DockerBuild configures an image build operation.
type DockerBuild struct {
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	BuildArgs  map[string]string `json:"build_args,omitempty"`
}

// DockerPush configures pushing an image to a registry.
type DockerPush struct {
	Image string `json:"image"`
}

// DockerPull configures pulling an image from a registry.
type DockerPull struct {
	Image string `json:"image"`
}

// DockerRun configures running a command in a new container.
type DockerRun struct {
	Image   string            `json:"image"`
	Name    string            `json:"name,omitempty"`
	Command []string          `json:"command,omitempty"`
	Ports   []string          `json:"ports,omitempty"`
	Volumes []string          `json:"volumes,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Detach  bool              `json:"detach,omitempty"`
}

// DockerExec configures executing a command inside a running container.
type DockerExec struct {
	Container string   `json:"container"`
	Command   []string `json:"command"`
}

// DockerStop configures stopping a running container.
type DockerStop struct {
	Container string `json:"container"`
}

// RecipeStepOpensearch configures an OpenSearch engine API step.
type RecipeStepOpensearch struct {
	Addresses []string       `json:"addresses,omitempty"`
	Username  string         `json:"username,omitempty"`
	Password  string         `json:"password,omitempty"`
	APIKey    string         `json:"api_key,omitempty"`
	Insecure  bool           `json:"insecure,omitempty"`
	Index     string         `json:"index"`
	Action    string         `json:"action"` // "get", "search", "index"
	DocID     string         `json:"doc_id,omitempty"`
	Body      map[string]any `json:"body,omitempty"`
	Output    string         `json:"output,omitempty"`
}

// RecipeStepPostgres configures a PostgreSQL engine API step.
type RecipeStepPostgres struct {
	DSNSecret     string            `json:"dsn_secret"`
	Action        string            `json:"action"` // "query", "exec", "migrate"
	SQL           string            `json:"sql,omitempty"`
	Params        json.RawMessage   `json:"params,omitempty"`
	TimeoutMS     int               `json:"timeout_ms,omitempty"`
	Readonly      *bool             `json:"readonly,omitempty"`
	KVKey         string            `json:"kv_key,omitempty"`
	KVKeyPerHost  bool              `json:"kv_key_per_host,omitempty"`
	Extract       map[string]string `json:"extract,omitempty"`
	Host          string            `json:"host,omitempty"`
	Port          string            `json:"port,omitempty"`
	TunnelStep    string            `json:"tunnel_step,omitempty"`
	MigrationsDir string            `json:"migrations_dir,omitempty"`
	Files         []string          `json:"files,omitempty"`
	Output        string            `json:"output,omitempty"`
}

// CanonicalJSON returns deterministic JSON (sorted keys, no extra
// whitespace) for the given Recipe. Two Recipes that resolve to the same plan
// produce the same bytes here.
func (r Recipe) CanonicalJSON() ([]byte, error) {
	raw, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("recipe canonical json: marshal: %w", err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("recipe canonical json: reparse: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(generic); err != nil {
		return nil, fmt.Errorf("recipe canonical json: re-encode: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// HashJSON returns "sha256:" + hex(sha256(r.CanonicalJSON())).
// Used to compare a recording's recipe to a disk recipe and decide "edited?".
func (r Recipe) HashJSON() (string, error) {
	b, err := r.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// RecipeFromJSON deserializes a canonical (or near-canonical) JSON payload back
// into a Recipe value. Run cuetry.ValidateRemoteRecipe (or the equivalent
// per-step validators) after this to ensure the result is well-formed.
func RecipeFromJSON(raw []byte) (Recipe, error) {
	var r Recipe
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return Recipe{}, fmt.Errorf("recipe from json: %w", err)
	}
	return r, nil
}

// WithRecipeDir attaches the absolute recipe directory to ctx (for age-file and similar).
func WithRecipeDir(ctx context.Context, absDir string) context.Context {
	return secrets.WithRecipeDir(ctx, absDir)
}
