package cuetry

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	"cuelang.org/go/cue/errors"

	"github.com/shareed2k/honey/internal/hosts"
)

// schemaSource defines the shape of a "remote recipe" document: a named list of
// host + shell command steps (similar in spirit to a tiny Ansible play).
const schemaSource = `
#Retry: close({
	attempts?:     int & >=1
	delay_ms?:     int & >=0
	max_delay_ms?: int & >=0
	backoff?: "fixed" | "exponential"
})
#StepHook: close({
	where: "local" | "remote"
	command?: string
	plugin?: close({
		id:     string
		action: string
		config?: {...}
	})
	run_as?: string
	env?: {[string]: string}
	secrets?: {[string]: string}
	notify?: close({
		notify_subject?: string
		message?:       string
		services?: close({
			http?:     close({})
			slack?:    close({
				channel_id?: string
			})
			telegram?: close({})
		})
	})
})
#Step: close({
	id?:      string
	depends?: [...string]
	host:     string
	ssh_port?: int
	ssh_private_key?: string
	notify?: close({
		notify_subject?: string
		message?:       string
		services?: close({
			http?:     close({})
			slack?:    close({
				channel_id?: string
			})
			telegram?: close({})
		})
	})
	run_as?:  string
	command?: string
	put?: close({
		local:  string
		remote: string
	})
	get?: close({
		local:  string
		remote: string
	})
	script?: close({
		local:  string
		remote: string
	})
	agent_transfer?: close({
		dest_host:    string
		source_path:  string
		dest_path:    string
		cloud: close({
			provider: string
			bucket:   string
			prefix?:  string
			object?:  string
			region?:  string
			endpoint?: string
		})
		cloud_backend_ref?: close({
			kind:  string
			name?:  string
			index?: int
		})
		keep_object?:      bool
		max_retries?:      int
		agent_remote_dir?: string
	})
	ai?: close({
		prompt:              string
		system_prompt?:      string
		model?:              string
		max_output_tokens?:  int
		max_input_chars?:    int
	})
	template?: close({
		template: string
		data?: {...}
		output?: string
	})
	plugin?: close({
		id:     string
		action: string
		config?: {...}
	})
	tunnel?: close({
		mode?: string
		remote_host?: string
		remote_port?: int
		local_port?: int
		bind?: string
		remote_bind?: string
		remote_listen_port?: int
		local_host?: string
		local_target_port?: int
		use_ssh_config?: bool
		ssh_config_match?: string
		ssh_config_env?: {[string]: string}
		share_key?: string
		protocol?: string
		tun_local?: int
		tun_remote?: int
		remote_socat?: bool
	})
	hooks?: close({
		on_success?: #StepHook
		on_failure?: #StepHook
	})
	kv_tunnel?: bool
	max_parallel?: int
	env_from?: [...close({
		step?: string
		from_output?: string
		map?: {[string]: string}
		extract?: {[string]: string}
		kv?: {[string]: string}
	})]
	env?: {[string]: string}
	secrets?: {[string]: string}
	when?: string
	retry?: #Retry
})
#Recipe: close({
	name:  string
	type?: "linear" | "graph"
	defaults?: close({
		run_as?: string
		env?: {[string]: string}
		secrets?: {[string]: string}
		k8s_debug_image?: string
		kv_tunnel?: bool
		max_parallel?: int
		ssh_port?: int
		ssh_private_key?: string
		retry?: #Retry
	})
	steps: [...#Step]
})
`

func compileAndUnifyRecipe(cueBytes []byte, records []hosts.Record) (cue.Value, error) {
	ctx := cuecontext.New()
	schema := ctx.CompileString(schemaSource)
	if err := schema.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: internal schema: %w", err)
	}

	var user cue.Value
	if len(records) > 0 {
		recordsVal := ctx.Encode(records)
		scope := ctx.CompileString("").FillPath(cue.ParsePath("hosts"), recordsVal)
		user = ctx.CompileBytes(cueBytes, cue.Filename("recipe.cue"), cue.Scope(scope))
	} else {
		user = ctx.CompileBytes(cueBytes, cue.Filename("recipe.cue"))
	}

	if err := user.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: parse: %w", formatCueErr(err))
	}

	recipe := user.LookupPath(cue.ParsePath("recipe"))
	if !recipe.Exists() {
		return cue.Value{}, fmt.Errorf("cuetry: missing top-level field \"recipe\"")
	}

	def := schema.LookupPath(cue.ParsePath("#Recipe"))
	if !def.Exists() {
		return cue.Value{}, fmt.Errorf("cuetry: internal schema missing #Recipe")
	}

	unified := def.Unify(recipe)
	if err := unified.Validate(cue.Concrete(true), cue.Final()); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: validate: %w", formatCueErr(err))
	}
	if err := unified.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: %w", formatCueErr(err))
	}
	return unified, nil
}

func validateDecodedRecipeStep(i, nSteps int, s RecipeStep, defaults *RecipeDefaults, records []hosts.Record, secretPrefixes []string, mode ExecutionMode) error {
	if err := ValidateHostField(s.Host); err != nil {
		return fmt.Errorf("cuetry: steps[%d].host: %w", i, err)
	}
	kind, err := ClassifyStep(s)
	if err != nil {
		return fmt.Errorf("cuetry: steps[%d]: %w", i, err)
	}
	if err := validateStepEnvAndSecrets(i, kind, s, secretPrefixes); err != nil {
		return err
	}
	if err := validateStepEnvFrom(i, kind, mode, s); err != nil {
		return err
	}
	if err := validateStepWhen(i, mode, s, nil); err != nil {
		return err
	}
	if err := validateMaxParallelField(fmt.Sprintf("steps[%d]", i), s.MaxParallel); err != nil {
		return err
	}
	if err := validateSSHPrivateKeyField(fmt.Sprintf("steps[%d]", i), s.SSHPrivateKey); err != nil {
		return err
	}
	if err := ValidateStepRunAsForKind(kind, s); err != nil {
		return fmt.Errorf("cuetry: steps[%d]: %w", i, err)
	}
	if strings.TrimSpace(s.RunAs) != "" {
		if err := ValidateRunAsUser(s.RunAs); err != nil {
			return fmt.Errorf("cuetry: steps[%d].run_as: %w", i, err)
		}
	}
	if kind == StepKindAgentTransfer {
		if err := validateAgentTransferStep(i, s, records); err != nil {
			return err
		}
	}
	if err := validateStepAI(i, nSteps, kind, s, mode); err != nil {
		return err
	}
	if err := validateStepTemplate(i, kind, s, mode); err != nil {
		return err
	}
	if err := validateStepTunnel(i, kind, s, mode); err != nil {
		return err
	}
	if err := validateStepRetry(i, s, defaults); err != nil {
		return err
	}
	return validateStepHooksAndKVTunnel(i, kind, s, defaults, secretPrefixes)
}

func validateStepRetry(i int, s RecipeStep, defaults *RecipeDefaults) error {
	if s.Retry != nil || (defaults != nil && defaults.Retry != nil) {
		cfg := EffectiveRetry(s, defaults)
		if err := ValidateRetry(cfg); err != nil {
			where := fmt.Sprintf("steps[%d].retry", i)
			if s.Retry == nil && defaults != nil && defaults.Retry != nil {
				where = "defaults.retry"
			}
			return fmt.Errorf("cuetry: %s: %w", where, err)
		}
	}
	return nil
}

func validateStepEnvAndSecrets(i int, kind StepKind, s RecipeStep, secretPrefixes []string) error {
	if len(s.Env) > 0 && (kind == StepKindPut || kind == StepKindGet || kind == StepKindAgentTransfer || kind == StepKindAI) {
		return fmt.Errorf("cuetry: steps[%d]: env is only supported for command, script, plugin, and template steps", i)
	}
	if len(s.Secrets) > 0 && kind != StepKindCommand && kind != StepKindScript && kind != StepKindPlugin && kind != StepKindTemplate {
		return fmt.Errorf("cuetry: steps[%d]: secrets are only supported for command, script, plugin, and template steps", i)
	}
	if len(s.Env) > 0 {
		if err := ValidateRecipeEnvMap(s.Env); err != nil {
			return fmt.Errorf("cuetry: steps[%d].env: %w", i, err)
		}
	}
	if len(s.Secrets) == 0 {
		return nil
	}
	if err := ValidateRecipeSecretsRefMapPrefixes(s.Secrets, secretPrefixes); err != nil {
		return fmt.Errorf("cuetry: steps[%d].secrets: %w", i, err)
	}
	if err := OverlapEnvSecrets(s.Env, s.Secrets); err != nil {
		return fmt.Errorf("cuetry: steps[%d]: %w", i, err)
	}
	return nil
}

func validateStepAI(i, nSteps int, kind StepKind, s RecipeStep, mode ExecutionMode) error {
	if kind != StepKindAI {
		return nil
	}
	if mode == ExecutionModeLinear && i != nSteps-1 {
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

func validateStepTemplate(i int, kind StepKind, s RecipeStep, mode ExecutionMode) error {
	if kind != StepKindTemplate {
		return nil
	}
	if err := ValidateHostField(s.Host); err != nil {
		return fmt.Errorf("cuetry: steps[%d].host: %w", i, err)
	}
	if s.Template == nil {
		return fmt.Errorf("cuetry: steps[%d]: internal template step", i)
	}
	if strings.TrimSpace(s.Template.Template) == "" {
		return fmt.Errorf("cuetry: steps[%d].template.template is required", i)
	}
	host := strings.TrimSpace(s.Host)
	outName := strings.TrimSpace(s.Template.Output)
	if outName != "" && host != MatchLocalAIHost {
		return fmt.Errorf("cuetry: steps[%d].template.output requires host %q (per-host templates cannot register a global capture name)", i, MatchLocalAIHost)
	}
	if mode == ExecutionModeGraph {
		if strings.TrimSpace(s.ID) == "" && (len(s.Depends) > 0 || len(s.EnvFrom) > 0) {
			return fmt.Errorf("cuetry: steps[%d]: template step with depends or env_from requires a non-empty id", i)
		}
	}
	return nil
}

func validateStepHooksAndKVTunnel(i int, kind StepKind, s RecipeStep, _ *RecipeDefaults, secretPrefixes []string) error {
	if s.Hooks != nil {
		if kind != StepKindCommand && kind != StepKindScript && kind != StepKindPlugin {
			return fmt.Errorf("cuetry: steps[%d]: hooks are only supported on command, script, and plugin steps", i)
		}
		if err := validateStepHooks(i, s.Hooks, secretPrefixes); err != nil {
			return err
		}
	}
	return nil
}

func parseRemoteRecipeAfterTransform(cueBytes []byte, records []hosts.Record, secretPrefixes []string) (Recipe, error) {
	var out Recipe
	unified, err := compileAndUnifyRecipe(cueBytes, records)
	if err != nil {
		return out, err
	}
	if err := unified.Decode(&out); err != nil {
		return out, fmt.Errorf("cuetry: decode: %w", err)
	}
	if out.Defaults != nil && strings.TrimSpace(out.Defaults.RunAs) != "" {
		if err := ValidateRunAsUser(out.Defaults.RunAs); err != nil {
			return out, fmt.Errorf("cuetry: defaults.run_as: %w", err)
		}
	}
	if out.Defaults != nil && len(out.Defaults.Env) > 0 {
		if err := ValidateRecipeEnvMap(out.Defaults.Env); err != nil {
			return out, fmt.Errorf("cuetry: defaults.env: %w", err)
		}
	}
	if out.Defaults != nil && len(out.Defaults.Secrets) > 0 {
		if err := ValidateRecipeSecretsRefMapPrefixes(out.Defaults.Secrets, secretPrefixes); err != nil {
			return out, fmt.Errorf("cuetry: defaults.secrets: %w", err)
		}
		if err := OverlapEnvSecrets(out.Defaults.Env, out.Defaults.Secrets); err != nil {
			return out, fmt.Errorf("cuetry: defaults: %w", err)
		}
	}
	if out.Defaults != nil {
		if err := validateSSHPrivateKeyField("defaults", out.Defaults.SSHPrivateKey); err != nil {
			return out, err
		}
		if err := validateMaxParallelField("defaults", out.Defaults.MaxParallel); err != nil {
			return out, err
		}
		if out.Defaults.Retry != nil {
			cfg := EffectiveRetry(RecipeStep{}, out.Defaults)
			if err := ValidateRetry(cfg); err != nil {
				return out, fmt.Errorf("cuetry: defaults.retry: %w", err)
			}
		}
	}
	mode, err := RecipeExecutionMode(out)
	if err != nil {
		return out, err
	}
	nSteps := len(out.Steps)
	for i, s := range out.Steps {
		if err := validateDecodedRecipeStep(i, nSteps, s, out.Defaults, records, secretPrefixes, mode); err != nil {
			return out, err
		}
	}
	if err := validateRecipeTunnelRefs(out.Steps); err != nil {
		return out, err
	}
	if err := ValidateRecipeGraph(out); err != nil {
		return out, err
	}
	return out, nil
}

func validateSSHPrivateKeyField(where, path string) error {
	if path == "" {
		return nil
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("cuetry: %s.ssh_private_key must not be empty or whitespace", where)
	}
	return nil
}

func validateStepHooks(stepIdx int, h *RecipeStepHooks, secretPrefixes []string) error {
	validateOne := func(phase string, hook *RecipeStepHook) error {
		if hook == nil {
			return nil
		}
		w := strings.TrimSpace(hook.Where)
		if w != "local" && w != "remote" {
			return fmt.Errorf("cuetry: steps[%d].hooks.%s.where must be \"local\" or \"remote\"", stepIdx, phase)
		}
		hasCmd := strings.TrimSpace(hook.Command) != ""
		hasPlugin := hook.Plugin != nil
		if w == "local" {
			if hasCmd == hasPlugin {
				return fmt.Errorf("cuetry: steps[%d].hooks.%s: exactly one of command or plugin is required for local hooks", stepIdx, phase)
			}
		} else if !hasCmd {
			return fmt.Errorf("cuetry: steps[%d].hooks.%s.command is required for remote hooks", stepIdx, phase)
		}
		if hasPlugin {
			if strings.TrimSpace(hook.Plugin.ID) == "" {
				return fmt.Errorf("cuetry: steps[%d].hooks.%s.plugin.id is required", stepIdx, phase)
			}
			if strings.TrimSpace(hook.Plugin.Action) == "" {
				return fmt.Errorf("cuetry: steps[%d].hooks.%s.plugin.action is required", stepIdx, phase)
			}
		}
		if w == "local" && strings.TrimSpace(hook.RunAs) != "" {
			return fmt.Errorf("cuetry: steps[%d].hooks.%s: run_as is not allowed when where is local", stepIdx, phase)
		}
		if len(hook.Env) > 0 {
			if err := ValidateRecipeEnvMap(hook.Env); err != nil {
				return fmt.Errorf("cuetry: steps[%d].hooks.%s.env: %w", stepIdx, phase, err)
			}
		}
		if len(hook.Secrets) > 0 {
			if err := ValidateRecipeSecretsRefMapPrefixes(hook.Secrets, secretPrefixes); err != nil {
				return fmt.Errorf("cuetry: steps[%d].hooks.%s.secrets: %w", stepIdx, phase, err)
			}
			if err := OverlapEnvSecrets(hook.Env, hook.Secrets); err != nil {
				return fmt.Errorf("cuetry: steps[%d].hooks.%s: %w", stepIdx, phase, err)
			}
		}
		if strings.TrimSpace(hook.RunAs) != "" {
			if err := ValidateRunAsUser(hook.RunAs); err != nil {
				return fmt.Errorf("cuetry: steps[%d].hooks.%s.run_as: %w", stepIdx, phase, err)
			}
		}
		return nil
	}
	if err := validateOne("on_success", h.OnSuccess); err != nil {
		return err
	}
	if err := validateOne("on_failure", h.OnFailure); err != nil {
		return err
	}
	return nil
}

func validateAgentTransferStep(i int, s RecipeStep, records []hosts.Record) error {
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
	if len(records) == 0 {
		return nil
	}
	src, err := ExpandStepHosts(s.Host, records)
	if err != nil {
		return fmt.Errorf("cuetry: steps[%d].host (source): %w", i, err)
	}
	if len(src) != 1 {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer: need exactly one source host match, got %d (narrow host selector)", i, len(src))
	}
	dst, err := ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer.dest_host: %w", i, err)
	}
	if len(dst) != 1 {
		return fmt.Errorf("cuetry: steps[%d].agent_transfer: need exactly one destination host match, got %d (narrow dest_host)", i, len(dst))
	}
	return nil
}

// ValidateRemoteRecipe checks that cueBytes is valid CUE and conforms to #Recipe.
func ValidateRemoteRecipe(cueBytes []byte) error {
	_, err := ParseRemoteRecipe(cueBytes, nil)
	return err
}

// ValidateParsedRecipe runs the same per-step validators that ParseRemoteRecipe
// applies after CUE decoding, but on an already-decoded Recipe value (e.g.
// constructed from JSON via RecipeFromJSON or supplied inline by an API caller).
// It does not re-parse CUE text, so callers that bypass the CUE compiler must
// invoke this to ensure the Recipe is well-formed before handing it to a runner.
func ValidateParsedRecipe(r Recipe, records []hosts.Record) error {
	if r.Defaults != nil && strings.TrimSpace(r.Defaults.RunAs) != "" {
		if err := ValidateRunAsUser(r.Defaults.RunAs); err != nil {
			return fmt.Errorf("cuetry: defaults.run_as: %w", err)
		}
	}
	if r.Defaults != nil && len(r.Defaults.Env) > 0 {
		if err := ValidateRecipeEnvMap(r.Defaults.Env); err != nil {
			return fmt.Errorf("cuetry: defaults.env: %w", err)
		}
	}
	if r.Defaults != nil && len(r.Defaults.Secrets) > 0 {
		if err := ValidateRecipeSecretsRefMap(r.Defaults.Secrets); err != nil {
			return fmt.Errorf("cuetry: defaults.secrets: %w", err)
		}
		if err := OverlapEnvSecrets(r.Defaults.Env, r.Defaults.Secrets); err != nil {
			return fmt.Errorf("cuetry: defaults: %w", err)
		}
	}
	if r.Defaults != nil {
		if err := validateMaxParallelField("defaults", r.Defaults.MaxParallel); err != nil {
			return err
		}
	}
	mode, err := RecipeExecutionMode(r)
	if err != nil {
		return err
	}
	nSteps := len(r.Steps)
	for i, s := range r.Steps {
		if err := validateDecodedRecipeStep(i, nSteps, s, r.Defaults, records, nil, mode); err != nil {
			return err
		}
	}
	if err := validateRecipeTunnelRefs(r.Steps); err != nil {
		return err
	}
	return ValidateRecipeGraph(r)
}

func formatCueErr(err error) error {
	if err == nil {
		return nil
	}
	var buf strings.Builder
	for _, e := range errors.Errors(err) {
		if buf.Len() > 0 {
			buf.WriteString("; ")
		}
		buf.WriteString(e.Error())
	}
	if buf.Len() == 0 {
		return err
	}
	return fmt.Errorf("%s", buf.String())
}
