package engine

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
)

func init() {
	RegisterStepExecutor(cuetry.KindRecipe, &RecipeExecutor{})
}

// RecipeExecutor executes the corresponding recipe step.
type RecipeExecutor struct{}

// ExecuteDryRun executes a dry run of the step.
func (e *RecipeExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error {
	out, i, step := out, req.Index, req.Step
	rs, _ := step.(*cuetry.RecipeStep)
	if rs == nil || rs.Recipe == nil {
		return fmt.Errorf("step %d: internal: missing recipe", i)
	}

	if !opts.Execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), opts.Recipe.Defaults))
		_, _ = fmt.Fprintf(out, "step %d: kind=recipe targets=%d → path:%q\n",
			i, len(req.Targets), rs.Recipe.Path)
		return nil
	}
	return nil
}

// ExecuteStream streams the step execution.
func (e *RecipeExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	step, targets, ch := req.Step, req.Targets, resCh
	rs, ok := step.(*cuetry.RecipeStep)
	if !ok || rs.Recipe == nil {
		return fmt.Errorf("internal: recipe step missing recipe field")
	}

	// Expand variables in the path (just in case they are used, but typically it's a static path)
	// Actually, the spec doesn't explicitly require var expansion for the path, but let's just use it as is.
	recipePath := rs.Recipe.Path
	subRecipePath, err := cuetry.ResolveLocalAgainstRecipe(opts.RecipeDir, recipePath)
	if err != nil {
		return fmt.Errorf("resolve recipe path: %w", err)
	}

	cueBytes, err := safepath.ReadFile(subRecipePath)
	if err != nil {
		return fmt.Errorf("failed to read sub-recipe %q: %w", recipePath, err)
	}

	var targetRecs []hosts.Record
	for _, tc := range targets {
		targetRecs = append(targetRecs, tc.Record)
	}

	subRecipe, err := cuetry.ParseRemoteRecipeOpts(cueBytes, targetRecs, cuetry.ParseOptions{
		PluginManager: opts.PluginMgr,
	})
	if err != nil {
		return fmt.Errorf("failed to parse sub-recipe %q: %w", recipePath, err)
	}

	// Combine parent's runtime environment for variable expansion
	parentVars := make(map[string]string)
	for k, v := range opts.CLIEnv {
		parentVars[k] = v
	}
	for k, v := range step.Base().Env {
		parentVars[k] = v
	}

	mergedEnv := make(map[string]string)
	for k, v := range opts.CLIEnv {
		mergedEnv[k] = v
	}
	for k, v := range rs.Recipe.Prompts {
		expanded, err := cuetry.ExpandRecipeVars(v, parentVars, false)
		if err != nil {
			return fmt.Errorf("expand prompt %q: %w", k, err)
		}
		mergedEnv[k] = expanded
	}

	subParams := CueRecipeRunParams{
		Recipe:         subRecipe,
		RecipeDir:      filepath.Dir(subRecipePath),
		Records:        targetRecs,
		SSHUser:        opts.SSHUser,
		CLIEnv:         mergedEnv,
		ConfigPath:     opts.ConfigPath,
		AISystemPrompt: opts.AISystemPrompt,
		SecretResolver: opts.SecretResolver,
		PluginMgr:      opts.PluginMgr,
		Execute:        opts.Execute,
		JSON:           opts.JSON,
		Reg:            opts.Reg,
		Obs:            opts.Obs,
		Pools:          opts.Pools,
		Cache:          opts.Cache,
	}

	return StreamCueRecipeSteps(ctx, subParams, ch)
}
