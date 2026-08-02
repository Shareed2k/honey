package plugins

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
)

// pluginCue holds a compiled plugin.cue source for a docker-runtime plugin.
// The source is re-evaluated per call against that call's config, since argv
// depends on per-call data — only the raw source (and a reusable *cue.Context)
// is cached, not a resolved value. Reuses the same cuecontext.New /
// ctx.Encode / ctx.CompileBytes(cue.Scope(...)) / LookupPath pattern already
// established in internal/cuetry/recipe.go's compileAndUnifyRecipe.
type pluginCue struct {
	ctx    *cue.Context
	source []byte
}

// actionResult is the evaluated shape of one plugin.cue action for a given config.
type actionResult struct {
	Argv         []string
	OutputFormat string // "json" or "text"
	// Env carries a per-call action's optional env field, evaluated from
	// config the same way Argv is. This is how a per-recipe secret (e.g. a
	// resolved DB password) reaches the exec'd process's environment instead
	// of its argv, keeping it out of `ps`/`/proc/<pid>/cmdline`.
	Env map[string]string
	// Stdin carries a per-call action's optional stdin field, evaluated from
	// config. This is how a JSON request body (e.g. an Elasticsearch query
	// DSL) reaches the exec'd process without shell-quoting pain — the body
	// is piped in, never embedded in an argv shell string.
	Stdin string
}

// newPluginCue compiles source once to catch syntax errors early. Returns an
// error if the source doesn't parse as CUE at all (per-action/per-config
// evaluation happens later, in evalAction, since it needs config bound in).
func newPluginCue(source []byte) (*pluginCue, error) {
	ctx := cuecontext.New()
	// Create a scope with an empty config to make config references valid during syntax check
	emptyConfig := ctx.Encode(map[string]any{})
	scope := ctx.CompileString("").FillPath(cue.ParsePath("config"), emptyConfig)
	v := ctx.CompileBytes(source, cue.Filename("plugin.cue"), cue.Scope(scope))
	if err := v.Err(); err != nil {
		return nil, fmt.Errorf("plugins: compile plugin.cue: %w", err)
	}
	return &pluginCue{ctx: ctx, source: source}, nil
}

// evalAction validates config against action's #Config schema (if present)
// and returns the evaluated argv and output_format.
func (pc *pluginCue) evalAction(action string, config map[string]any) (actionResult, error) {
	configVal := pc.ctx.Encode(config)
	scope := pc.ctx.CompileString("").FillPath(cue.ParsePath("config"), configVal)
	doc := pc.ctx.CompileBytes(pc.source, cue.Filename("plugin.cue"), cue.Scope(scope))
	// We do not check doc.Err() here immediately because other actions might fail to compile
	// if they depend on fields not present in this action's config.

	actionVal := doc.LookupPath(cue.ParsePath("actions." + action))
	if !actionVal.Exists() {
		return actionResult{}, fmt.Errorf("plugins: plugin.cue: unknown action %q", action)
	}

	// Validate config against schema and apply defaults
	if cfgDef := actionVal.LookupPath(cue.ParsePath("#Config")); cfgDef.Exists() {
		unified := cfgDef.Unify(configVal)
		if err := unified.Validate(cue.Concrete(true), cue.Final()); err != nil {
			return actionResult{}, fmt.Errorf("plugins: action %q: config validation: %w", action, err)
		}

		// Decode the unified config back to a map to extract the concrete values (including defaults).
		// Note: This relies on fields being required-with-default (e.g. `field: string | *"default"`)
		// rather than optional (`field?: string | *"default"`), as CUE Decode omits uninstantiated optional fields.
		var resolvedConfig map[string]any
		if err := unified.Decode(&resolvedConfig); err != nil {
			return actionResult{}, fmt.Errorf("plugins: action %q: decode unified config: %w", action, err)
		}

		// Re-evaluate with the resolved config
		configVal = pc.ctx.Encode(resolvedConfig)
		scope = pc.ctx.CompileString("").FillPath(cue.ParsePath("config"), configVal)
		doc = pc.ctx.CompileBytes(pc.source, cue.Filename("plugin.cue"), cue.Scope(scope))

		actionVal = doc.LookupPath(cue.ParsePath("actions." + action))
	}

	argvVal := actionVal.LookupPath(cue.ParsePath("argv"))
	if !argvVal.Exists() {
		return actionResult{}, fmt.Errorf("plugins: plugin.cue: action %q missing argv", action)
	}
	var argv []string
	if err := argvVal.Decode(&argv); err != nil {
		return actionResult{}, fmt.Errorf("plugins: action %q: decode argv: %w", action, err)
	}

	outputFormat := "text"
	if ofVal := actionVal.LookupPath(cue.ParsePath("output_format")); ofVal.Exists() {
		var of string
		if err := ofVal.Decode(&of); err != nil {
			return actionResult{}, fmt.Errorf("plugins: action %q: decode output_format: %w", action, err)
		}
		of = strings.TrimSpace(of)
		if of != "" {
			outputFormat = of
		}
	}

	env := map[string]string{}
	if envVal := actionVal.LookupPath(cue.ParsePath("env")); envVal.Exists() {
		if err := envVal.Decode(&env); err != nil {
			return actionResult{}, fmt.Errorf("plugins: action %q: decode env: %w", action, err)
		}
	}

	var stdin string
	if stdinVal := actionVal.LookupPath(cue.ParsePath("stdin")); stdinVal.Exists() {
		if err := stdinVal.Decode(&stdin); err != nil {
			return actionResult{}, fmt.Errorf("plugins: action %q: decode stdin: %w", action, err)
		}
	}

	return actionResult{Argv: argv, OutputFormat: outputFormat, Env: env, Stdin: stdin}, nil
}
