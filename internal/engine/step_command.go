package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

func init() {
	RegisterStepExecutor(cuetry.KindCommand, &CommandExecutor{})
	RegisterStepExecutor(cuetry.KindPut, &PutExecutor{})
	RegisterStepExecutor(cuetry.KindGet, &GetExecutor{})
	RegisterStepExecutor(cuetry.KindScript, &ScriptExecutor{})
}

// CommandExecutor executes the corresponding recipe step.
type CommandExecutor struct{}

// PutExecutor executes the corresponding recipe step.
type PutExecutor struct{}

// GetExecutor executes the corresponding recipe step.
type GetExecutor struct{}

// ScriptExecutor executes the corresponding recipe step.
type ScriptExecutor struct{}

// ExecuteStream streams the step execution.
func (e *CommandExecutor) ExecuteStream(sc *StepContext) error {
	run, ctx, stepIdx, kind, step, targets, ch, retryCfg, attemptMax := sc.Run, sc.Ctx, sc.Index, sc.Kind, sc.Step, sc.Targets, sc.ResultCh, sc.RetryCfg, sc.AttemptMax
	cs, ok := step.(*cuetry.CommandStep)
	if !ok {
		return fmt.Errorf("internal: command step has wrong type %T", step)
	}
	b := step.Base()
	runAs := cuetry.EffectiveRunAs(b, run.Params.Recipe.Defaults)
	kvTunnel := cuetry.KVTunnelEnabled(step, run.Params.Recipe.Defaults)

	cmdFunc := func(r hosts.Record, kv map[string]string) string {
		env, err := cuetry.EffectiveEnvForRunEx(ctx, true, run.Params.SecretResolver, b, run.Params.Recipe.Defaults, run.Params.CLIEnv, &r, CueEnvRunOpts(&run.Params.Recipe, run.OutputStore, run.OutputCapture, KvReaderFromCoordinator(run.RecipeKV), false))
		if err != nil {
			return fmt.Sprintf("echo 'env err: %s'", err.Error())
		}
		for k, v := range kv {
			env[k] = v
		}
		mainCmd := strings.TrimSpace(cs.Command)
		if interp := strings.TrimSpace(cs.Interpreter); interp != "" {
			mainCmd = fmt.Sprintf("%s -c %s", interp, ShellSingleQuoted(mainCmd))
		}
		var combined string
		if b.CheckCmd != "" {
			combined = fmt.Sprintf("if %s; then echo 'HONEY_CHECK_CMD_OK'; else %s; fi", strings.TrimSpace(b.CheckCmd), mainCmd)
		} else {
			combined = mainCmd
		}
		if strings.TrimSpace(b.Timeout) != "" {
			d, err := time.ParseDuration(strings.TrimSpace(b.Timeout))
			if err == nil && d > 0 {
				combined = fmt.Sprintf(
					"command -v timeout >/dev/null 2>&1 || { echo '__HONEY_TIMEOUT_MISSING__' >&2; exit 124; }; timeout %s sh -c %s",
					d.String(),
					ShellSingleQuoted(combined),
				)
			}
		}
		inner, err := cuetry.ShellExportPrefixForRemote(env, combined)
		if err != nil {
			return fmt.Sprintf("echo 'export err: %s'", err.Error())
		}
		remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
		if err != nil {
			return fmt.Sprintf("echo 'wrap err: %s'", err.Error())
		}
		return remoteCmd
	}

	recipeScoped := kvTunnel
	post := CueRecipeSSHPostHostResult(ctx, run, stepIdx, kind, step, recipeScoped)
	return StreamSSHParallel(ctx, run.Params.SSHUser, targets, kvTunnel, cmdFunc, ch, BatchOptions{
		MaxConc:        RecipeHostMaxConc(step, run.Params.Recipe.Defaults),
		Cache:          run.Cache,
		RecipeKV:       run.RecipeKV,
		RecipeScopedKV: recipeScoped,
		Post:           post,
		RetryCfg:       retryCfg,
		Obs:            run.Params.Obs,
		AttemptMax:     attemptMax,
		Reg:            run.Params.Reg,
	})
}

// ExecuteStream streams the step execution.
func (e *PutExecutor) ExecuteStream(sc *StepContext) error {
	run, ctx, step, targets, ch, retryCfg, attemptMax := sc.Run, sc.Ctx, sc.Step, sc.Targets, sc.ResultCh, sc.RetryCfg, sc.AttemptMax
	ps, ok := step.(*cuetry.PutStep)
	if !ok || ps.Put == nil {
		return fmt.Errorf("internal: put step missing put field")
	}

	// Expand variables in the paths
	localExpanded, err := cuetry.ExpandRecipeVars(ps.Put.Local, run.Params.CLIEnv, false)
	if err != nil {
		return fmt.Errorf("put.local var expansion: %w", err)
	}
	remoteExpanded, err := cuetry.ExpandRecipeVars(ps.Put.Remote, run.Params.CLIEnv, false)
	if err != nil {
		return fmt.Errorf("put.remote var expansion: %w", err)
	}

	localAbs, err := cuetry.ResolveLocalAgainstRecipe(run.Params.RecipeDir, localExpanded)
	if err != nil {
		return fmt.Errorf("put.local: %w", err)
	}
	remotePath := strings.TrimSpace(remoteExpanded)
	if _, statErr := os.Stat(localAbs); statErr != nil {
		return fmt.Errorf("put: local file %q: %w", localAbs, statErr)
	}
	return StreamSFTPUploadParallel(ctx, run.Params.SSHUser, targets, localAbs, remotePath, ch, BatchOptions{
		MaxConc:    RecipeHostMaxConc(step, run.Params.Recipe.Defaults),
		Cache:      run.Cache,
		RetryCfg:   retryCfg,
		Obs:        run.Params.Obs,
		AttemptMax: attemptMax,
	})
}

// ExecuteStream streams the step execution.
func (e *GetExecutor) ExecuteStream(sc *StepContext) error {
	run, ctx, step, targets, ch, retryCfg, attemptMax := sc.Run, sc.Ctx, sc.Step, sc.Targets, sc.ResultCh, sc.RetryCfg, sc.AttemptMax
	gs, okt := step.(*cuetry.GetStep)
	if !okt || gs.Get == nil {
		return fmt.Errorf("internal: get step missing get field")
	}

	// Expand variables in the paths
	localExpanded, err := cuetry.ExpandRecipeVars(gs.Get.Local, run.Params.CLIEnv, false)
	if err != nil {
		return fmt.Errorf("get.local var expansion: %w", err)
	}
	remoteExpanded, err := cuetry.ExpandRecipeVars(gs.Get.Remote, run.Params.CLIEnv, false)
	if err != nil {
		return fmt.Errorf("get.remote var expansion: %w", err)
	}

	remotePath := strings.TrimSpace(remoteExpanded)
	localRoot, err := cuetry.ResolveLocalAgainstRecipe(run.Params.RecipeDir, localExpanded)
	if err != nil {
		return fmt.Errorf("get.local: %w", err)
	}
	if len(targets) > 1 {
		ok, err := CueGetLocalIsDirectory(localExpanded, localRoot)
		if err != nil {
			return fmt.Errorf("get: %w", err)
		} else if !ok {
			return fmt.Errorf("get: %d hosts require get.local to be a directory; got %q", len(targets), gs.Get.Local)
		}
	}
	jobs := make([]SFTPDownloadJob, 0, len(targets))
	base := filepath.Base(remotePath)
	if base == "." || base == "/" {
		base = "download"
	}
	for _, target := range targets {
		dest := localRoot
		if len(targets) > 1 {
			dest = filepath.Join(localRoot, CueSanitizeHostName(target.Name)+"_"+base)
		}
		jobs = append(jobs, SFTPDownloadJob{
			Record:    target,
			LocalAbs:  dest,
			RemoteAbs: remotePath,
		})
	}
	if len(targets) > 1 {
		if err := os.MkdirAll(localRoot, 0o750); err != nil {
			return fmt.Errorf("get: mkdir %q: %w", localRoot, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(jobs[0].LocalAbs), 0o750); err != nil {
			return fmt.Errorf("get: mkdir parent: %w", err)
		}
	}
	return StreamSFTPDownloadParallel(ctx, run.Params.SSHUser, jobs, ch, BatchOptions{
		MaxConc:    RecipeHostMaxConc(step, run.Params.Recipe.Defaults),
		Cache:      run.Cache,
		RetryCfg:   retryCfg,
		Obs:        run.Params.Obs,
		AttemptMax: attemptMax,
	})
}

// ExecuteStream streams the step execution.
func (e *ScriptExecutor) ExecuteStream(sc *StepContext) error {
	run, ctx, stepIdx, kind, step, targets, ch, retryCfg, attemptMax := sc.Run, sc.Ctx, sc.Index, sc.Kind, sc.Step, sc.Targets, sc.ResultCh, sc.RetryCfg, sc.AttemptMax
	ss, ok := step.(*cuetry.ScriptStep)
	if !ok || ss.Script == nil {
		return fmt.Errorf("internal: script step missing script field")
	}

	// Expand variables in the paths
	localExpanded, err := cuetry.ExpandRecipeVars(ss.Script.Local, run.Params.CLIEnv, false)
	if err != nil {
		return fmt.Errorf("script.local var expansion: %w", err)
	}
	remoteExpanded, err := cuetry.ExpandRecipeVars(ss.Script.Remote, run.Params.CLIEnv, false)
	if err != nil {
		return fmt.Errorf("script.remote var expansion: %w", err)
	}

	b := step.Base()
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(run.Params.RecipeDir, localExpanded)
	if err != nil {
		return fmt.Errorf("script.local: %w", err)
	}
	remotePath := strings.TrimSpace(remoteExpanded)
	runAs := cuetry.EffectiveRunAs(b, run.Params.Recipe.Defaults)
	kvTunnel := cuetry.KVTunnelEnabled(step, run.Params.Recipe.Defaults)

	if _, statErr := os.Stat(localAbs); statErr != nil {
		return fmt.Errorf("script: local file %q: %w", localAbs, statErr)
	}

	cmdFunc := func(r hosts.Record, kv map[string]string) string {
		env, err := cuetry.EffectiveEnvForRunEx(ctx, true, run.Params.SecretResolver, b, run.Params.Recipe.Defaults, run.Params.CLIEnv, &r, CueEnvRunOpts(&run.Params.Recipe, run.OutputStore, run.OutputCapture, KvReaderFromCoordinator(run.RecipeKV), false))
		if err != nil {
			return fmt.Sprintf("echo 'env err: %s'", err.Error())
		}
		for k, v := range kv {
			env[k] = v
		}
		remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs, env, ss.Interpreter)
		if err != nil {
			return fmt.Sprintf("echo 'wrap err: %s'", err.Error())
		}
		finalCmd := remoteCmd
		if b.CheckCmd != "" {
			finalCmd = fmt.Sprintf("if %s; then echo 'HONEY_CHECK_CMD_OK'; else %s; fi", strings.TrimSpace(b.CheckCmd), remoteCmd)
		}
		if strings.TrimSpace(b.Timeout) != "" {
			d, err := time.ParseDuration(strings.TrimSpace(b.Timeout))
			if err == nil && d > 0 {
				finalCmd = fmt.Sprintf(
					"command -v timeout >/dev/null 2>&1 || { echo '__HONEY_TIMEOUT_MISSING__' >&2; exit 124; }; timeout %s sh -c %s",
					d.String(),
					ShellSingleQuoted(finalCmd),
				)
			}
		}
		return finalCmd
	}

	recipeScoped := kvTunnel
	post := CueRecipeSSHPostHostResult(ctx, run, stepIdx, kind, step, recipeScoped)
	return StreamScriptUploadRunParallel(ctx, run.Params.SSHUser, targets, localAbs, remotePath, kvTunnel, cmdFunc, ch, BatchOptions{
		MaxConc:        RecipeHostMaxConc(step, run.Params.Recipe.Defaults),
		Cache:          run.Cache,
		RecipeKV:       run.RecipeKV,
		RecipeScopedKV: recipeScoped,
		Post:           post,
		RetryCfg:       retryCfg,
		Obs:            run.Params.Obs,
		AttemptMax:     attemptMax,
	})
}

// ExecuteDryRun executes a dry run of the step.
func (e *CommandExecutor) ExecuteDryRun(sc *StepContext) error {
	out, recipe, execute, cliEnv, i, step, targets := sc.Out, sc.Recipe, sc.Execute, sc.CLIEnv, sc.Index, sc.Step, sc.Targets
	cs, _ := step.(*cuetry.CommandStep)
	command := ""
	if cs != nil {
		command = cs.Command
		if interp := strings.TrimSpace(cs.Interpreter); interp != "" {
			command = fmt.Sprintf("%s -c %s", interp, ShellSingleQuoted(command))
		}
	}
	runAs := cuetry.EffectiveRunAs(step.Base(), recipe.Defaults)
	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueKVTunnelDryLine(out, recipe, i, step, recipe.Defaults)
		WriteCueSSHPrivateKeyDryLine(out, i, step, recipe.Defaults)
		WriteCueStepHooksDryLines(out, i, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), recipe.Defaults))
		for _, target := range targets {
			env, err := cuetry.EffectiveEnvForRunEx(context.Background(), false, nil, step.Base(), recipe.Defaults, cliEnv, &target, CueEnvRunOpts(&recipe, nil, nil, nil, true))
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			inner, err := cuetry.ShellExportPrefixForRemote(env, strings.TrimSpace(command))
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}

			_, _ = fmt.Fprintf(out, "step %d: kind=command name=%q %s provider=%s run_as=%q remote=%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, runAs, remoteCmd)
		}
		return nil
	}
	return nil
}

// ExecuteDryRun executes a dry run of the step.
func (e *PutExecutor) ExecuteDryRun(sc *StepContext) error {
	out, recipe, recipeDir, execute, i, step, targets := sc.Out, sc.Recipe, sc.RecipeDir, sc.Execute, sc.Index, sc.Step, sc.Targets
	ps, _ := step.(*cuetry.PutStep)
	if ps == nil || ps.Put == nil {
		return fmt.Errorf("step %d: internal: missing put", i)
	}
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, ps.Put.Local)
	if err != nil {
		return fmt.Errorf("step %d put.local: %w", i, err)
	}
	remotePath := strings.TrimSpace(ps.Put.Remote)
	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), recipe.Defaults))
		if _, statErr := os.Stat(localAbs); statErr != nil {
			_, _ = fmt.Fprintf(out, "step %d: kind=put (warning: local not readable: %v)\n", i, statErr)
		}
		for _, target := range targets {
			_, _ = fmt.Fprintf(out, "step %d: kind=put name=%q %s provider=%s %q → remote:%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, localAbs, remotePath)
		}
		return nil
	}
	return nil
}

// ExecuteDryRun executes a dry run of the step.
func (e *GetExecutor) ExecuteDryRun(sc *StepContext) error {
	out, recipe, recipeDir, execute, i, step, targets := sc.Out, sc.Recipe, sc.RecipeDir, sc.Execute, sc.Index, sc.Step, sc.Targets
	gs, _ := step.(*cuetry.GetStep)
	if gs == nil || gs.Get == nil {
		return fmt.Errorf("step %d: internal: missing get", i)
	}
	remotePath := strings.TrimSpace(gs.Get.Remote)
	localRoot, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, gs.Get.Local)
	if err != nil {
		return fmt.Errorf("step %d get.local: %w", i, err)
	}
	if len(targets) > 1 {
		ok, err := CueGetLocalIsDirectory(gs.Get.Local, localRoot)
		if err != nil {
			return fmt.Errorf("step %d get: %w", i, err)
		}
		if !ok {
			return fmt.Errorf("step %d get: %d hosts require get.local to be a directory (add trailing %q or use an existing directory); got %q",
				i, len(targets), string(filepath.Separator), gs.Get.Local)
		}
	}
	jobs := make([]SFTPDownloadJob, 0, len(targets))
	base := filepath.Base(remotePath)
	if base == "." || base == "/" {
		base = "download"
	}
	for _, target := range targets {
		dest := localRoot
		if len(targets) > 1 {
			dest = filepath.Join(localRoot, CueSanitizeHostName(target.Name)+"_"+base)
		}
		jobs = append(jobs, SFTPDownloadJob{
			Record:    target,
			LocalAbs:  dest,
			RemoteAbs: remotePath,
		})
	}
	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), recipe.Defaults))
		for _, j := range jobs {
			_, _ = fmt.Fprintf(out, "step %d: kind=get name=%q %s provider=%s remote:%q → %q\n",
				i, j.Record.Name, FormatTargetForDryRun(j.Record), j.Record.Provider, j.RemoteAbs, j.LocalAbs)
		}
		return nil
	}
	return nil
}

// ExecuteDryRun executes a dry run of the step.
func (e *ScriptExecutor) ExecuteDryRun(sc *StepContext) error {
	out, recipeDir, recipe, execute, cliEnv, i, step, targets := sc.Out, sc.RecipeDir, sc.Recipe, sc.Execute, sc.CLIEnv, sc.Index, sc.Step, sc.Targets
	ss, _ := step.(*cuetry.ScriptStep)
	if ss == nil || ss.Script == nil {
		return fmt.Errorf("step %d: internal: missing script", i)
	}
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, ss.Script.Local)
	if err != nil {
		return fmt.Errorf("step %d script.local: %w", i, err)
	}
	remotePath := strings.TrimSpace(ss.Script.Remote)
	runAs := cuetry.EffectiveRunAs(step.Base(), recipe.Defaults)

	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueKVTunnelDryLine(out, recipe, i, step, recipe.Defaults)
		WriteCueSSHPrivateKeyDryLine(out, i, step, recipe.Defaults)
		WriteCueStepHooksDryLines(out, i, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), recipe.Defaults))
		if _, statErr := os.Stat(localAbs); statErr != nil {
			_, _ = fmt.Fprintf(out, "step %d: kind=script (warning: local not readable: %v)\n", i, statErr)
		}
		for _, target := range targets {
			env, err := cuetry.EffectiveEnvForRunEx(context.Background(), false, nil, step.Base(), recipe.Defaults, cliEnv, &target, CueEnvRunOpts(&recipe, nil, nil, nil, true))
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs, env, ss.Interpreter)
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			_, _ = fmt.Fprintf(out, "step %d: kind=script name=%q %s provider=%s put %q → %q then exec run_as=%q cmd=%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, localAbs, remotePath, runAs, remoteCmd)
		}
		return nil
	}
	return nil
}
