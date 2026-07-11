package cuetry

import (
	"fmt"
	"strings"
	"sync"

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
	where?: "local" | "remote"
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
	trigger_rule?: "all_success" | "one_failed" | "all_done"
	rescue?:  [...string]
	matrix?: {[string]: [...string]}
	assert?: [...{
		regex?: string
		not_regex?: string
		json_path?: string
		expected_value?: string
		exit_code?: int
	}]
	host?:    string
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
	interpreter?: string
	templated?: bool
	render?:  string
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
	summarize?: close({
		prompt:              string
		system_prompt?:      string
		model?:              string
		max_output_tokens?:  int
		max_input_chars?:    int
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
	opa?: close({
		policy: string
		input?: {...}
	})
	plugin?: close({
		id:     string
		action: string
		config?: {...}
		kv_key?:         string
		kv_key_per_host?: bool
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
	docker?: close({
		socket?: string
		action: "build" | "push" | "pull" | "run" | "exec" | "stop"
		output?: string
		build?: {
			context:     string
			dockerfile?: string
			tags?: [...string]
			build_args?: {[string]: string}
		}
		push?: {
			image: string
		}
		pull?: {
			image: string
		}
		run?: {
			image:   string
			name?:    string
			command?: [...string]
			ports?: [...string]
			volumes?: [...string]
			env?: {[string]: string}
			detach?: bool
			rm?:     bool
		}
		exec?: {
			container: string
			command: [...string]
		}
		stop?: {
			container: string
			rm?:        bool
		}
	})
	k8s?: close({
		namespace?: string
		output?:    string
		apply?: close({
			manifest:     string
			force?:       bool
			server_side?: bool
		})
		delete?: close({
			resource: string
			wait?:    bool
		})
		scale?: close({
			resource: string
			replicas: int & >=0
		})
		rollout_restart?: close({
			resource: string
			wait?:    bool
		})
		wait?: close({
			resource: string
			"for":    string
			timeout?: string
		})
		get?: close({
			resource:       string
			label_selector?: string
			format?:        "json" | "yaml" | "name"
		})
		exec?: close({
			pod:        string
			container?: string
			command:    [...string]
			tty?:       bool
		})
		create_job?: close({
			name:             string
			image:            string
			command?:         [...string]
			args?:            [...string]
			env?:             {[string]: string}
			restart_policy?:  "Never" | "OnFailure"
			wait?:            bool
			ttl_seconds?:     int
		})
	})
	opensearch?: close({
		addresses?: [...string]
		username?:  string
		password?:  string
		api_key?:   string
		insecure?:  bool
		index:      string
		action:     "get" | "search" | "index"
		doc_id?:    string
		body?:      {...}
		output?:    string
	})
	postgres?: close({
		dsn_secret:      string
		action:          "query" | "exec" | "migrate"
		sql?:            string
		params?:         [...]
		timeout_ms?:     int
		readonly?:       bool
		kv_key?:         string
		kv_key_per_host?: bool
		extract?:        {[string]: string}
		host?:           string
		port?:           string
		tunnel_step?:    string
		migrations_dir?: string
		files?:          [...string]
		output?:         string
	})
	package?: close({
		name:  string
		state: "present" | "absent" | "latest"
	})
	service?: close({
		name:    string
		state:   "started" | "stopped" | "restarted" | "reloaded" | "status"
		enabled?: bool
	})
	recipe?: close({
		path:     string
		prompts?: {[string]: string}
	})
	hooks?: close({
		on_success?: #StepHook
		on_failure?: #StepHook
	})
	kv_tunnel?: bool
	max_parallel?: int
	serial?: int & >=1
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
	changed_when?: string
	failed_when?: string
	retry?: #Retry
	timeout?:       string
	ignore_errors?: bool
	check_cmd?:     string
	output?:        string
	loop?:          string
	loop_from?: close({
		step:    string
		extract: string
	})
	reduce?:        string
	notify_handler?: [...string]
})
#Webhook: close({
	auth_secret?:     string
	actor?:           string
	extract?:         {[string]: string}
	async?:           bool
	idempotency_key?: string
	idempotency_ttl?: string
})
#Schedule: close({
	cron:      string
	timezone?: string
	env?:      {[string]: string}
})
#Recipe: close({
	name:  string
	type?: "linear" | "graph"
	webhooks?:  {[string]: #Webhook}
	schedules?: {[string]: #Schedule}
	notification?: close({
		email?: close({
			send_on?: close({
				success?: bool
				failure?: bool
			})
			on_success?: close({
				from: string
				to: [...string]
				prefix?: string
			})
			on_failure?: close({
				from: string
				to: [...string]
				prefix?: string
				attach_logs?: bool
			})
		})
	})
	defaults?: close({
		run_as?: string
		env?: {[string]: string}
		secrets?: {[string]: string}
		prompts?: {[string]: close({
			description?: string
			type?: string
			required?: bool
			choices?: [...string]
			choices_url?: string
			choices_json_path?: string
			default?: string
			multi?: bool
			regex?: string
		})}
		k8s_debug_image?: string
		kv_tunnel?: bool
		max_parallel?: int
		ssh_port?: int
		ssh_private_key?: string
		retry?: #Retry
		gather_facts?: bool
	})
	steps: [...#Step]
	handlers?: [...#Step]
})
`

// schemaCtx holds a pre-compiled CUE context and #Recipe schema value.
// Each instance owns its own *cue.Context so there is no shared mutable state
// between concurrent borrows.  uses is incremented on every return to the pool;
// once it reaches maxCtxReuse the entry is dropped (GC reclaims it) so that
// CUE's per-context interning map does not grow without bound.
type schemaCtx struct {
	ctx  *cue.Context
	def  cue.Value // #Recipe compiled in ctx
	uses int
}

const maxCtxReuse = 256

var schemaCtxPool = sync.Pool{New: func() any { return newSchemaCtx() }}

func newSchemaCtx() *schemaCtx {
	ctx := cuecontext.New()
	schema := ctx.CompileString(schemaSource)
	// schemaSource is a package-level constant; a compile error here is a
	// programming error with no recovery path — panic is acceptable.
	if err := schema.Err(); err != nil {
		panic(fmt.Sprintf("cuetry: internal schema compile error: %v", err))
	}
	def := schema.LookupPath(cue.ParsePath("#Recipe"))
	if !def.Exists() {
		panic("cuetry: internal schema missing #Recipe")
	}
	return &schemaCtx{ctx: ctx, def: def}
}

func compileAndUnifyRecipe(cueBytes []byte, records []hosts.Record) (cue.Value, error) {
	sc := schemaCtxPool.Get().(*schemaCtx)
	defer func() {
		sc.uses++
		if sc.uses >= maxCtxReuse {
			return // drop; GC reclaims, pool.New makes a fresh one next time
		}
		schemaCtxPool.Put(sc)
	}()
	ctx := sc.ctx

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

	unified := sc.def.Unify(recipe)
	if err := unified.Validate(cue.Concrete(true), cue.Final()); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: validate: %w", formatCueErr(err))
	}
	if err := unified.Err(); err != nil {
		return cue.Value{}, fmt.Errorf("cuetry: %w", formatCueErr(err))
	}
	return unified, nil
}

func validateDecodedRecipeStep(vc StepValidateCtx, w StepWrapper) error {
	st := w.Step
	b := st.Base()
	kind := st.Kind()
	i := vc.Index
	if err := validateStepHost(i, kind, b); err != nil {
		return err
	}
	if err := validateStepEnvAndSecrets(i, kind, b, vc.SecretPrefixes); err != nil {
		return err
	}
	if err := validateStepEnvFrom(i, kind, vc.Mode, b); err != nil {
		return err
	}
	if err := validateStepWhen(i, vc.Mode, b, nil); err != nil {
		return err
	}
	if rs, ok := st.(RemoteStep); ok {
		r := rs.Remote()
		if err := validateMaxParallelField(fmt.Sprintf("steps[%d]", i), r.MaxParallel); err != nil {
			return err
		}
		if r.Serial < 0 {
			return fmt.Errorf("cuetry: steps[%d].serial must be >= 1", i)
		}
		if err := validateSSHPrivateKeyField(fmt.Sprintf("steps[%d]", i), r.SSHPrivateKey); err != nil {
			return err
		}
	}
	if err := validateStepRunAs(i, b); err != nil {
		return err
	}
	if err := validateStepRetry(i, b, vc.Defaults); err != nil {
		return err
	}
	if err := validateStepLoop(i, b); err != nil {
		return err
	}
	if err := validateStepOutputAndResultExpr(i, b); err != nil {
		return err
	}
	if err := validateStepHooksAndKVTunnel(i, kind, b, vc.SecretPrefixes); err != nil {
		return err
	}
	return st.Validate(vc)
}

func validateStepHost(i int, kind string, b *StepBase) error {
	// Template hosts (including render-only steps) are validated in TemplateStep.Validate.
	if kind == KindTemplate {
		return nil
	}
	if err := ValidateHostField(b.Host); err != nil {
		return fmt.Errorf("cuetry: steps[%d].host: %w", i, err)
	}
	return nil
}

// validateStepRunAs validates run_as syntax for kinds that support it. Whether
// a given kind supports run_as at all is enforced by that step's own
// Validate() (short deny-list of 10 kinds — concentrating it there matches
// CONTEXT.md's per-kind-adapter design; see architecture review candidate #7).
func validateStepRunAs(i int, b *StepBase) error {
	runAs := strings.TrimSpace(b.RunAs)
	if runAs == "" {
		return nil
	}
	if err := ValidateRunAsUser(runAs); err != nil {
		return fmt.Errorf("cuetry: steps[%d].run_as: %w", i, err)
	}
	return nil
}

func validateStepLoop(i int, b *StepBase) error {
	if strings.TrimSpace(b.Loop) != "" && b.LoopFrom != nil {
		return fmt.Errorf("cuetry: steps[%d]: only one of loop or loop_from may be set", i)
	}
	return nil
}

func validateStepOutputAndResultExpr(i int, b *StepBase) error {
	if out := strings.TrimSpace(b.Output); out != "" {
		if !recipeStepIDPattern.MatchString(out) {
			return fmt.Errorf("cuetry: steps[%d].output %q must match [a-zA-Z][a-zA-Z0-9_-]*", i, out)
		}
	}
	if expr := strings.TrimSpace(b.ChangedWhen); expr != "" {
		if _, err := CompileResultBoolExpr(expr); err != nil {
			return fmt.Errorf("cuetry: steps[%d].changed_when: %w", i, err)
		}
	}
	if expr := strings.TrimSpace(b.FailedWhen); expr != "" {
		if _, err := CompileResultBoolExpr(expr); err != nil {
			return fmt.Errorf("cuetry: steps[%d].failed_when: %w", i, err)
		}
	}
	return nil
}

func validateStepRetry(i int, b *StepBase, defaults *RecipeDefaults) error {
	if b.Retry != nil || (defaults != nil && defaults.Retry != nil) {
		cfg := EffectiveRetry(b, defaults)
		if err := ValidateRetry(cfg); err != nil {
			where := fmt.Sprintf("steps[%d].retry", i)
			if b.Retry == nil && defaults != nil && defaults.Retry != nil {
				where = "defaults.retry"
			}
			return fmt.Errorf("cuetry: %s: %w", where, err)
		}
	}
	return nil
}

// validateStepEnvAndSecrets validates env/secrets syntax and the secrets
// allow-list. env's own deny-list (agent_transfer, summarize — only 2 of 19 kinds) is
// enforced by those steps' own Validate() instead (see architecture review
// candidate #7). secrets stays gated here: its allow-list is short (4 of 19
// kinds), so this single check is already more concentrated than distributing
// it across the other 15 kinds would be.
func validateStepEnvAndSecrets(i int, kind string, b *StepBase, secretPrefixes []string) error {
	if len(b.Secrets) > 0 && kind != KindCommand && kind != KindScript && kind != KindPlugin && kind != KindTemplate {
		return fmt.Errorf("cuetry: steps[%d]: secrets are only supported for command, script, plugin, and template steps", i)
	}
	if len(b.Env) > 0 {
		if err := ValidateRecipeEnvMap(b.Env); err != nil {
			return fmt.Errorf("cuetry: steps[%d].env: %w", i, err)
		}
	}
	if len(b.Secrets) == 0 {
		return nil
	}
	if err := ValidateRecipeSecretsRefMapPrefixes(b.Secrets, secretPrefixes); err != nil {
		return fmt.Errorf("cuetry: steps[%d].secrets: %w", i, err)
	}
	if err := OverlapEnvSecrets(b.Env, b.Secrets); err != nil {
		return fmt.Errorf("cuetry: steps[%d]: %w", i, err)
	}
	return nil
}

func validateStepHooksAndKVTunnel(i int, kind string, b *StepBase, secretPrefixes []string) error {
	if b.Hooks != nil {
		if kind != KindCommand && kind != KindScript && kind != KindPlugin {
			return fmt.Errorf("cuetry: steps[%d]: hooks are only supported on command, script, and plugin steps", i)
		}
		if err := validateStepHooks(i, b.Hooks, secretPrefixes); err != nil {
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
	defaultRenderHosts(out.Steps)
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
			cfg := EffectiveRetry(&StepBase{}, out.Defaults)
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
	for i, st := range out.Steps {
		vc := StepValidateCtx{Index: i, NumSteps: nSteps, Defaults: out.Defaults, Records: records, SecretPrefixes: secretPrefixes, Mode: mode}
		if err := validateDecodedRecipeStep(vc, st); err != nil {
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
		w := EffectiveHookWhere(hook)
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
	for i, st := range r.Steps {
		vc := StepValidateCtx{Index: i, NumSteps: nSteps, Defaults: r.Defaults, Records: records, Mode: mode}
		if err := validateDecodedRecipeStep(vc, st); err != nil {
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
