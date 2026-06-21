package engine

import (
	"fmt"
	"path/filepath"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
)

func init() {
	RegisterStepExecutor(cuetry.KindRecipe, &RecipeExecutor{})
}

// RecipeExecutor executes the corresponding recipe step.
type RecipeExecutor struct{}

// ExecuteDryRun executes a dry run of the step.
func (e *RecipeExecutor) ExecuteDryRun(sc *StepContext) error {
	out, i, step := sc.Out, sc.Index, sc.Step
	rs, _ := step.(*cuetry.RecipeStep)
	if rs == nil || rs.Recipe == nil {
		return fmt.Errorf("step %d: internal: missing recipe", i)
	}

	if !sc.Execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), sc.Recipe.Defaults))
		_, _ = fmt.Fprintf(out, "step %d: kind=recipe targets=%d → path:%q\n",
			i, len(sc.Targets), rs.Recipe.Path)
		return nil
	}
	return nil
}

// ExecuteStream streams the step execution.
func (e *RecipeExecutor) ExecuteStream(sc *StepContext) error {
	run, ctx, step, targets, ch := sc.Run, sc.Ctx, sc.Step, sc.Targets, sc.ResultCh
	rs, ok := step.(*cuetry.RecipeStep)
	if !ok || rs.Recipe == nil {
		return fmt.Errorf("internal: recipe step missing recipe field")
	}

	// Expand variables in the path (just in case they are used, but typically it's a static path)
	// Actually, the spec doesn't explicitly require var expansion for the path, but let's just use it as is.
	recipePath := rs.Recipe.Path
	subRecipePath, err := cuetry.ResolveLocalAgainstRecipe(sc.RecipeDir, recipePath)
	if err != nil {
		return fmt.Errorf("resolve recipe path: %w", err)
	}

	cueBytes, err := safepath.ReadFile(subRecipePath)
	if err != nil {
		return fmt.Errorf("failed to read sub-recipe %q: %w", recipePath, err)
	}

	subRecipe, err := cuetry.ParseRemoteRecipeOpts(cueBytes, targets, cuetry.ParseOptions{
		PluginManager: sc.PluginMgr,
	})
	if err != nil {
		return fmt.Errorf("failed to parse sub-recipe %q: %w", recipePath, err)
	}

	mergedEnv := make(map[string]string)
	for k, v := range sc.CLIEnv {
		mergedEnv[k] = v
	}
	for k, v := range rs.Recipe.Prompts {
		mergedEnv[k] = v
	}

	subParams := CueRecipeRunParams{
		Recipe:         subRecipe,
		RecipeDir:      filepath.Dir(subRecipePath),
		Records:        targets,
		SSHUser:        sc.SSHUser,
		CLIEnv:         mergedEnv,
		ConfigPath:     sc.ConfigPath,
		AISystemPrompt: sc.AISystemPrompt,
		SecretResolver: sc.SecretResolver,
		PluginMgr:      sc.PluginMgr,
		Execute:        sc.Execute,
		JSON:           run.Params.JSON,
		Reg:            run.Params.Reg,
		Obs:            run.Params.Obs,
		Pools:          run.Params.Pools,
		Cache:          run.Cache,
	}

	return StreamCueRecipeSteps(ctx, subParams, ch)
}
