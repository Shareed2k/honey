package cuetry

import (
	"fmt"
	"strings"
)

// This file defines the concrete Step implementations. Each embeds StepBase (shared
// cross-cutting fields) and, for remotely-executed kinds, RemoteExec. Validate handles
// only the kind-specific rules — the shared validation chain runs in
// validateDecodedRecipeStep, so it is not duplicated here (see plan Trap E).

// ---------------------------------------------------------------------------
// command
// ---------------------------------------------------------------------------

// CommandStep runs a shell command on remote hosts.
type CommandStep struct {
	StepBase
	RemoteExec
	Command     string `json:"command,omitempty"`
	Interpreter string `json:"interpreter,omitempty"`
	Templated   bool   `json:"templated,omitempty"`
}

// Kind returns the step kind identifier.
func (s *CommandStep) Kind() string { return KindCommand }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *CommandStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *CommandStep) Validate(_ StepValidateCtx) error {
	if s.Templated {
		if err := validateTemplateSyntax(s.Command); err != nil {
			return fmt.Errorf("command: %w", err)
		}
	}
	return nil
}

var _ RemoteStep = (*CommandStep)(nil)

// ---------------------------------------------------------------------------
// script
// ---------------------------------------------------------------------------

// ScriptStep uploads a local script and executes it on remote hosts.
type ScriptStep struct {
	StepBase
	RemoteExec
	Script      *RecipeFileTransfer `json:"script,omitempty"`
	Interpreter string              `json:"interpreter,omitempty"`
	Templated   bool                `json:"templated,omitempty"`
}

// Kind returns the step kind identifier.
func (s *ScriptStep) Kind() string { return KindScript }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *ScriptStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
// Templated is not syntax-checked here (unlike CommandStep): the rendered
// body is the local script *file's content*, not a field available at
// recipe-parse time — a bad template only surfaces at render/execute time.
func (s *ScriptStep) Validate(_ StepValidateCtx) error {
	if s.Script == nil {
		return fmt.Errorf("script step requires a script file transfer")
	}
	return validateFileTransfer("script", s.Script)
}

var _ RemoteStep = (*ScriptStep)(nil)

// ---------------------------------------------------------------------------
// put / get
// ---------------------------------------------------------------------------

// PutStep uploads a local file to remote hosts via SFTP.
type PutStep struct {
	StepBase
	RemoteExec
	Put *RecipeFileTransfer `json:"put,omitempty"`
}

// Kind returns the step kind identifier.
func (s *PutStep) Kind() string { return KindPut }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *PutStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *PutStep) Validate(vc StepValidateCtx) error {
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on put steps is not supported", vc.Index)
	}
	if s.Put == nil {
		return fmt.Errorf("put step requires a file transfer")
	}
	return validateFileTransfer("put", s.Put)
}

var _ RemoteStep = (*PutStep)(nil)

// GetStep downloads a remote file to the local machine via SFTP.
type GetStep struct {
	StepBase
	RemoteExec
	Get *RecipeFileTransfer `json:"get,omitempty"`
}

// Kind returns the step kind identifier.
func (s *GetStep) Kind() string { return KindGet }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *GetStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *GetStep) Validate(vc StepValidateCtx) error {
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on get steps is not supported", vc.Index)
	}
	if s.Get == nil {
		return fmt.Errorf("get step requires a file transfer")
	}
	return validateFileTransfer("get", s.Get)
}

var _ RemoteStep = (*GetStep)(nil)

// ---------------------------------------------------------------------------
// plugin
// ---------------------------------------------------------------------------

// PluginStep invokes a WASM custom_step plugin.
type PluginStep struct {
	StepBase
	RemoteExec
	Plugin *RecipeStepPlugin `json:"plugin,omitempty"`
}

// Kind returns the step kind identifier.
func (s *PluginStep) Kind() string { return KindPlugin }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *PluginStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *PluginStep) Validate(_ StepValidateCtx) error {
	if s.Plugin == nil {
		return fmt.Errorf("plugin step requires a plugin block")
	}
	if strings.TrimSpace(s.Plugin.ID) == "" {
		return fmt.Errorf("plugin.id is required")
	}
	if strings.TrimSpace(s.Plugin.Action) == "" {
		return fmt.Errorf("plugin.action is required")
	}
	if key := strings.TrimSpace(s.Plugin.KVKey); key != "" {
		if err := stepkvValidateKey(key); err != nil {
			return fmt.Errorf("plugin.kv_key %q: %w", key, err)
		}
	}
	return nil
}

var _ RemoteStep = (*PluginStep)(nil)

// ---------------------------------------------------------------------------
// tunnel
// ---------------------------------------------------------------------------

// TunnelStep establishes SSH port forwarding.
type TunnelStep struct {
	StepBase
	RemoteExec
	Tunnel *RecipeStepTunnel `json:"tunnel,omitempty"`
}

// Kind returns the step kind identifier.
func (s *TunnelStep) Kind() string { return KindTunnel }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *TunnelStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *TunnelStep) Validate(vc StepValidateCtx) error {
	i := vc.Index
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on tunnel steps is not supported", i)
	}
	if s.Tunnel == nil {
		return fmt.Errorf("cuetry: steps[%d]: internal tunnel step", i)
	}
	t := s.Tunnel
	tm := EffectiveTunnelMode(t)
	if err := validateLoopbackBind(fmt.Sprintf("steps[%d].tunnel.bind", i), t.Bind); err != nil {
		return err
	}
	if err := validateLoopbackBind(fmt.Sprintf("steps[%d].tunnel.remote_bind", i), t.RemoteBind); err != nil {
		return err
	}
	if sk := strings.TrimSpace(t.ShareKey); sk != "" {
		if len(sk) > 128 {
			return fmt.Errorf("cuetry: steps[%d].tunnel.share_key exceeds 128 chars", i)
		}
	}
	switch tm {
	case "local", "udp":
		if !t.UseSSHConfig && t.RemotePort <= 0 {
			return fmt.Errorf("cuetry: steps[%d].tunnel.remote_port is required unless use_ssh_config is true", i)
		}
		if tm == "udp" && !t.RemoteSocat {
			return fmt.Errorf("cuetry: steps[%d].tunnel.remote_socat must be true for udp mode", i)
		}
	case "remote":
		if !t.UseSSHConfig && (t.RemoteListen <= 0 || t.LocalTarget <= 0) {
			return fmt.Errorf("cuetry: steps[%d].tunnel requires remote_listen_port and local_target_port for remote mode", i)
		}
	case "dynamic":
	case "tun":
		if t.TunLocal < 0 || t.TunRemote < 0 {
			return fmt.Errorf("cuetry: steps[%d].tunnel tun ids must be non-negative", i)
		}
	default:
		return fmt.Errorf("cuetry: steps[%d].tunnel.mode %q is invalid", i, tm)
	}
	if t.LocalPort < 0 || t.LocalPort >= 65536 {
		return fmt.Errorf("cuetry: steps[%d].tunnel.local_port out of range", i)
	}
	if vc.Mode == ExecutionModeGraph {
		id := strings.TrimSpace(s.ID)
		if id == "" && (len(s.Depends) > 0 || len(s.EnvFrom) > 0) {
			return fmt.Errorf("cuetry: steps[%d]: tunnel step with depends or env_from requires id", i)
		}
	}
	return nil
}

var _ RemoteStep = (*TunnelStep)(nil)

// ---------------------------------------------------------------------------
// k8s / docker / opensearch / postgres
// ---------------------------------------------------------------------------

// K8sStep performs a Kubernetes API action.
type K8sStep struct {
	StepBase
	RemoteExec
	K8s *RecipeStepK8s `json:"k8s,omitempty"`
}

// Kind returns the step kind identifier.
func (s *K8sStep) Kind() string { return KindK8s }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *K8sStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *K8sStep) Validate(vc StepValidateCtx) error {
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on k8s steps is not supported", vc.Index)
	}
	if s.K8s == nil {
		return fmt.Errorf("k8s step requires a k8s block")
	}
	return validateK8sStep(s.K8s)
}

var _ RemoteStep = (*K8sStep)(nil)

// DockerStep performs a Docker engine action.
type DockerStep struct {
	StepBase
	RemoteExec
	Docker *RecipeStepDocker `json:"docker,omitempty"`
}

// Kind returns the step kind identifier.
func (s *DockerStep) Kind() string { return KindDocker }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *DockerStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *DockerStep) Validate(_ StepValidateCtx) error {
	if s.Docker == nil {
		return fmt.Errorf("docker step requires a docker block")
	}
	return validateDockerStep(s.Docker)
}

var _ RemoteStep = (*DockerStep)(nil)

// OpensearchStep performs an OpenSearch API action.
type OpensearchStep struct {
	StepBase
	RemoteExec
	Opensearch *RecipeStepOpensearch `json:"opensearch,omitempty"`
}

// Kind returns the step kind identifier.
func (s *OpensearchStep) Kind() string { return KindOpensearch }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *OpensearchStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *OpensearchStep) Validate(vc StepValidateCtx) error {
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on opensearch steps is not supported", vc.Index)
	}
	if s.Opensearch == nil {
		return fmt.Errorf("opensearch step requires an opensearch block")
	}
	return validateOpensearchStep(s.Opensearch)
}

var _ RemoteStep = (*OpensearchStep)(nil)

// PostgresStep performs a PostgreSQL action.
type PostgresStep struct {
	StepBase
	RemoteExec
	Postgres *RecipeStepPostgres `json:"postgres,omitempty"`
}

// Kind returns the step kind identifier.
func (s *PostgresStep) Kind() string { return KindPostgres }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *PostgresStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *PostgresStep) Validate(vc StepValidateCtx) error {
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on postgres steps is not supported", vc.Index)
	}
	if s.Postgres == nil {
		return fmt.Errorf("postgres step requires a postgres block")
	}
	return validatePostgresStep(s.Postgres)
}

var _ RemoteStep = (*PostgresStep)(nil)

// ---------------------------------------------------------------------------
// recipe
// ---------------------------------------------------------------------------

// RecipeStep executes another recipe as a sub-recipe.
type RecipeStep struct {
	StepBase
	Recipe *RecipeSubRecipe `json:"recipe,omitempty"`
}

// Kind returns the step kind identifier.
func (s *RecipeStep) Kind() string { return KindRecipe }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *RecipeStep) Clone() Step {
	cp := *s
	cp.StepBase = s.cloned()
	if s.Recipe != nil {
		rCp := *s.Recipe
		if rCp.Prompts != nil {
			rCp.Prompts = make(map[string]string, len(s.Recipe.Prompts))
			for k, v := range s.Recipe.Prompts {
				rCp.Prompts[k] = v
			}
		}
		cp.Recipe = &rCp
	}
	return &cp
}

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *RecipeStep) Validate(vc StepValidateCtx) error {
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on recipe steps is not supported", vc.Index)
	}
	if s.Recipe == nil {
		return fmt.Errorf("recipe step requires a recipe block")
	}
	if strings.TrimSpace(s.Recipe.Path) == "" {
		return fmt.Errorf("recipe.path is required")
	}
	return nil
}

var _ Step = (*RecipeStep)(nil)

// ---------------------------------------------------------------------------
// agent_transfer
// ---------------------------------------------------------------------------

// AgentTransferStep stages a file through cloud storage from a source to a dest host.
type AgentTransferStep struct {
	StepBase
	RemoteExec
	AgentTransfer *RecipeAgentTransfer `json:"agent_transfer,omitempty"`
}

// Kind returns the step kind identifier.
func (s *AgentTransferStep) Kind() string { return KindAgentTransfer }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *AgentTransferStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *AgentTransferStep) Validate(vc StepValidateCtx) error {
	i := vc.Index
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on agent_transfer steps is not supported", i)
	}
	if len(s.Env) > 0 {
		return fmt.Errorf("cuetry: steps[%d]: env is not supported for agent_transfer steps", i)
	}
	at := s.AgentTransfer
	if at == nil {
		return fmt.Errorf("cuetry: steps[%d]: internal agent_transfer", i)
	}
	if err := ValidateHostField(at.DestHost); err != nil {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.dest_host: %w", i, err)
	}
	if strings.TrimSpace(at.SourcePath) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.source_path is empty", i)
	}
	if strings.TrimSpace(at.DestPath) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.dest_path is empty", i)
	}
	if at.Cloud == nil {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.cloud is required", i)
	}
	if strings.TrimSpace(at.Cloud.Provider) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.cloud.provider is empty", i)
	}
	if strings.TrimSpace(at.Cloud.Bucket) == "" {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.cloud.bucket is empty", i)
	}
	if len(vc.Records) == 0 {
		return nil
	}
	src, err := ExpandStepHosts(s.Host, vc.Records)
	if err != nil {
		return fmt.Errorf("cuetry: steps[%d].host (source): %w", i, err)
	}
	if len(src) != 1 {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer: need exactly one source host match, got %d (narrow host selector)", i, len(src))
	}
	dst, err := ExpandStepHosts(at.DestHost, vc.Records)
	if err != nil {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.dest_host: %w", i, err)
	}
	if len(dst) != 1 {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer: need exactly one destination host match, got %d (narrow dest_host)", i, len(dst))
	}
	return nil
}

var _ RemoteStep = (*AgentTransferStep)(nil)

// ---------------------------------------------------------------------------
// template (local) — no RemoteExec, so its schema has no ssh/fan-out fields
// ---------------------------------------------------------------------------

// TemplateStep renders a Go text/template locally.
type TemplateStep struct {
	StepBase
	Template *RecipeStepTemplate `json:"template,omitempty"`
	Render   string              `json:"render,omitempty"`
}

// Kind returns the step kind identifier.
func (s *TemplateStep) Kind() string { return KindTemplate }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *TemplateStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *TemplateStep) Validate(vc StepValidateCtx) error {
	i := vc.Index
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on template steps is not supported", i)
	}
	// host: a render step with empty host is allowed (defaults applied later).
	if strings.TrimSpace(s.Host) != "" || strings.TrimSpace(s.Render) == "" {
		if err := ValidateHostField(s.Host); err != nil {
			return fmt.Errorf("cuetry: steps[%d].host: %w", i, err)
		}
	}
	if s.Template == nil && strings.TrimSpace(s.Render) == "" {
		return fmt.Errorf("cuetry: steps[%d]: internal template step", i)
	}
	if s.Template != nil && strings.TrimSpace(s.Template.Template) == "" {
		return fmt.Errorf("cuetry: steps[%d].template.template is required", i)
	}
	host := strings.TrimSpace(s.Host)
	outName := ""
	if s.Template != nil {
		outName = strings.TrimSpace(s.Template.Output)
	}
	if outName != "" && host != MatchLocalAIHost {
		return fmt.Errorf("cuetry: steps[%d].template.output requires host %q (per-host templates cannot register a global capture name)", i, MatchLocalAIHost)
	}
	if vc.Mode == ExecutionModeGraph {
		if strings.TrimSpace(s.ID) == "" && (len(s.Depends) > 0 || len(s.EnvFrom) > 0) {
			return fmt.Errorf("cuetry: steps[%d]: template step with depends or env_from requires a non-empty id", i)
		}
	}
	return nil
}

var _ Step = (*TemplateStep)(nil)

// ---------------------------------------------------------------------------
// ai (local) — no RemoteExec
// ---------------------------------------------------------------------------

// AIStep runs the terminal local LLM summarizer (must be last; host must be "_").
type AIStep struct {
	StepBase
	AI *RecipeAI `json:"ai,omitempty"`
}

// Kind returns the step kind identifier.
func (s *AIStep) Kind() string { return KindAI }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *AIStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *AIStep) Validate(vc StepValidateCtx) error {
	i := vc.Index
	if strings.TrimSpace(s.RunAs) != "" {
		return fmt.Errorf("cuetry: steps[%d]: run_as on ai steps is not supported", i)
	}
	if len(s.Env) > 0 {
		return fmt.Errorf("cuetry: steps[%d]: env is not supported for ai steps", i)
	}
	if vc.Mode == ExecutionModeLinear && i != vc.NumSteps-1 {
		return fmt.Errorf("cuetry: steps[%d]: ai step must be the last step in the recipe", i)
	}
	if i == 0 {
		return fmt.Errorf("cuetry: steps[%d]: ai cannot be the first step; add at least one prior step", i)
	}
	if strings.TrimSpace(s.Host) != MatchLocalAIHost {
		return fmt.Errorf("cuetry: steps[%d]: ai step host must be %q", i, MatchLocalAIHost)
	}
	if s.AI == nil {
		return fmt.Errorf("cuetry: steps[%d]: internal ai step", i)
	}
	if strings.TrimSpace(s.AI.Prompt) == "" {
		return fmt.Errorf("cuetry: steps[%d].ai.prompt is required", i)
	}
	return nil
}

var _ Step = (*AIStep)(nil)

// ---------------------------------------------------------------------------
// opa (local) — no RemoteExec
// ---------------------------------------------------------------------------

// OPAStep evaluates an OPA/rego policy inline during recipe execution. It runs
// locally (host must be "_") and fails when the policy denies, so authors can
// gate later steps via depends / when on this step's success.
type OPAStep struct {
	StepBase
	OPA *RecipeOPA `json:"opa,omitempty"`
}

// RecipeOPA is the opa step's action block: which policy to load and what extra
// input to pass alongside the actor identity.
type RecipeOPA struct {
	// Policy is a path to a .rego file (package honey), relative to the recipe
	// directory unless absolute.
	Policy string `json:"policy"`
	// Input is an arbitrary object merged into the OPA input document under the
	// caller-supplied keys, alongside the built-in actor and recipe fields.
	Input map[string]any `json:"input,omitempty"`
}

// Kind returns the step kind identifier.
func (s *OPAStep) Kind() string { return KindOPA }

// Clone returns a deep copy of the step (safe for loop fan-out mutation).
func (s *OPAStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields; shared rules run separately.
func (s *OPAStep) Validate(vc StepValidateCtx) error {
	i := vc.Index
	if s.OPA == nil {
		return fmt.Errorf("cuetry: steps[%d]: internal opa step", i)
	}
	if strings.TrimSpace(s.OPA.Policy) == "" {
		return fmt.Errorf("cuetry: steps[%d].opa.policy is required", i)
	}
	if strings.TrimSpace(s.Host) != MatchLocalAIHost {
		return fmt.Errorf("cuetry: steps[%d]: opa step host must be %q", i, MatchLocalAIHost)
	}
	return nil
}

var _ Step = (*OPAStep)(nil)

// ---------------------------------------------------------------------------
// package
// ---------------------------------------------------------------------------

// PackageStep manages system packages.
type PackageStep struct {
	StepBase
	RemoteExec
	Package *RecipeStepPackage `json:"package,omitempty"`
}

// Kind returns the step kind identifier.
func (s *PackageStep) Kind() string { return KindPackage }

// Clone returns a deep copy of the step.
func (s *PackageStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields.
func (s *PackageStep) Validate(vc StepValidateCtx) error {
	if s.Package == nil {
		return fmt.Errorf("cuetry: steps[%d].package is required", vc.Index)
	}
	if s.Package.Name == "" {
		return fmt.Errorf("cuetry: steps[%d].package.name is required", vc.Index)
	}
	switch s.Package.State {
	case "present", "absent", "latest":
	default:
		return fmt.Errorf("cuetry: steps[%d].package.state must be present, absent, or latest", vc.Index)
	}
	return nil
}

var _ RemoteStep = (*PackageStep)(nil)

// ---------------------------------------------------------------------------
// service
// ---------------------------------------------------------------------------

// ServiceStep manages system services.
type ServiceStep struct {
	StepBase
	RemoteExec
	Service *RecipeStepService `json:"service,omitempty"`
}

// Kind returns the step kind identifier.
func (s *ServiceStep) Kind() string { return KindService }

// Clone returns a deep copy of the step.
func (s *ServiceStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields.
func (s *ServiceStep) Validate(vc StepValidateCtx) error {
	if s.Service == nil {
		return fmt.Errorf("cuetry: steps[%d].service is required", vc.Index)
	}
	if s.Service.Name == "" {
		return fmt.Errorf("cuetry: steps[%d].service.name is required", vc.Index)
	}
	switch s.Service.State {
	case "started", "stopped", "restarted", "reloaded", "status":
	default:
		return fmt.Errorf("cuetry: steps[%d].service.state must be started, stopped, restarted, reloaded, or status", vc.Index)
	}
	return nil
}

var _ RemoteStep = (*ServiceStep)(nil)

// ---------------------------------------------------------------------------
// aws
// ---------------------------------------------------------------------------

// AwsStep interacts with AWS APIs.
type AwsStep struct {
	StepBase
	Aws *RecipeStepAws `json:"aws,omitempty"`
}

// Kind returns the step kind identifier.
func (s *AwsStep) Kind() string { return KindAws }

// Clone returns a deep copy of the step.
func (s *AwsStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields.
func (s *AwsStep) Validate(vc StepValidateCtx) error {
	if s.Aws == nil {
		return fmt.Errorf("cuetry: steps[%d].aws is required", vc.Index)
	}
	return nil
}

// ---------------------------------------------------------------------------
// gcp
// ---------------------------------------------------------------------------

// GcpStep interacts with GCP APIs.
type GcpStep struct {
	StepBase
	Gcp *RecipeStepGcp `json:"gcp,omitempty"`
}

// Kind returns the step kind identifier.
func (s *GcpStep) Kind() string { return KindGcp }

// Clone returns a deep copy of the step.
func (s *GcpStep) Clone() Step { cp := *s; cp.StepBase = s.cloned(); return &cp }

// Validate checks this step's kind-specific fields.
func (s *GcpStep) Validate(vc StepValidateCtx) error {
	if s.Gcp == nil {
		return fmt.Errorf("cuetry: steps[%d].gcp is required", vc.Index)
	}
	return nil
}

// ---------------------------------------------------------------------------
// registration
// ---------------------------------------------------------------------------

func init() {
	RegisterStep(KindCommand, []string{"command"}, func() Step { return &CommandStep{} })
	RegisterStep(KindScript, []string{"script"}, func() Step { return &ScriptStep{} })
	RegisterStep(KindPut, []string{"put"}, func() Step { return &PutStep{} })
	RegisterStep(KindGet, []string{"get"}, func() Step { return &GetStep{} })
	RegisterStep(KindTemplate, []string{"template", "render"}, func() Step { return &TemplateStep{} })
	RegisterStep(KindPlugin, []string{"plugin"}, func() Step { return &PluginStep{} })
	RegisterStep(KindTunnel, []string{"tunnel"}, func() Step { return &TunnelStep{} })
	RegisterStep(KindK8s, []string{"k8s"}, func() Step { return &K8sStep{} })
	RegisterStep(KindDocker, []string{"docker"}, func() Step { return &DockerStep{} })
	RegisterStep(KindOpensearch, []string{"opensearch"}, func() Step { return &OpensearchStep{} })
	RegisterStep(KindPostgres, []string{"postgres"}, func() Step { return &PostgresStep{} })
	RegisterStep(KindPackage, []string{"package"}, func() Step { return &PackageStep{} })
	RegisterStep(KindService, []string{"service"}, func() Step { return &ServiceStep{} })
	RegisterStep(KindAws, []string{"aws"}, func() Step { return &AwsStep{} })
	RegisterStep(KindGcp, []string{"gcp"}, func() Step { return &GcpStep{} })
	RegisterStep(KindRecipe, []string{"recipe"}, func() Step { return &RecipeStep{} })
	RegisterStep(KindAgentTransfer, []string{"agent_transfer"}, func() Step { return &AgentTransferStep{} })
	RegisterStep(KindAI, []string{"ai"}, func() Step { return &AIStep{} })
	RegisterStep(KindOPA, []string{"opa"}, func() Step { return &OPAStep{} })
}
