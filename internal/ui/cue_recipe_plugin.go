package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/stepkv"
	"go.uber.org/zap"
)

func streamCueStepPlugin(ctx context.Context, run *cueRun, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, targets []hosts.Record, ch chan<- HostExecResult, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) error {
	if run.PluginMgr == nil || !run.PluginMgr.Enabled() {
		return fmt.Errorf("plugin step requires plugins.enabled in honey config")
	}
	pl := step.Plugin
	if pl == nil {
		return fmt.Errorf("internal plugin step")
	}
	maxConc := recipeHostMaxConc(step, run.Recipe.Defaults)
	if maxConc <= 0 {
		maxConc = 8
	}
	sem := make(chan struct{}, maxConc)
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := runCuePluginOnHost(ctx, run, stepIdx, kind, step, target, retryCfg, attemptMax)
			ch <- res
		}()
	}
	wg.Wait()
	return nil
}

func runCuePluginOnHost(ctx context.Context, run *cueRun, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, target hosts.Record, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) HostExecResult {
	pluginMgr := run.PluginMgr
	obs := run.Obs
	res := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
	hostJSON, err := json.Marshal(target)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	secretsDry := !run.Execute
	env, err := cuetry.EffectiveEnvForRunEx(ctx, run.Execute, run.SecretResolver, step, run.Recipe.Defaults, run.CLIEnv, &target, cueEnvRunOpts(&run.Recipe, run.outputStore, run.outputCapture, kvReaderFromCoordinator(run.recipeKV), secretsDry))
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	var kvSess *stepkv.Session
	if run.Execute && run.recipeKV != nil {
		kvSess, err = run.recipeKV.EnsureSession()
		if err != nil {
			res.ErrMsg = err.Error()
			return res
		}
	}
	expanded, err := cuetry.ExpandPluginConfigJSON(step.Plugin.Config, env, secretsDry)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	pluginConfig, err := RewritePluginConfigTunnelStep(expanded, step.Plugin.ID, run.tunnelCoord, run.SSHUser, target, run.Execute)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	runAs := cuetry.EffectiveRunAs(step, run.Recipe.Defaults)
	bridge := NewRemoteBridge(run.SSHUser, target, run.cache, run.Reg, run.RecipeDir, runAs, env, pluginMgr.EffectivePaths(step.Plugin.ID))
	hostCtx := &plugins.HostRunContext{
		SSHUser:              run.SSHUser,
		Record:               target,
		RecipeDir:            run.RecipeDir,
		Execute:              run.Execute,
		SecretsDry:           secretsDry,
		RunAs:                runAs,
		Env:                  env,
		Bridge:               bridge,
		AllowedPaths:         pluginMgr.EffectivePaths(step.Plugin.ID),
		RecipeSecrets:        mergeRecipeSecretRefs(run.Recipe.Defaults, step),
		PluginID:             step.Plugin.ID,
		MaxPostgresTimeoutMS: pluginMgr.TimeoutMS(),
	}
	if run.tunnelCoord != nil {
		hostCtx.TunnelCoord = run.tunnelCoord
	}
	if run.SecretResolver != nil {
		hostCtx.ResolveSecret = run.SecretResolver.Resolve
	}
	hostCtx.Postgres = NewPostgresBridge(hostCtx, run.Pools)
	callCtx := plugins.WithHostRunContext(ctx, hostCtx)
	if kvSess != nil {
		callCtx = plugins.WithKVSession(callCtx, kvSess)
	}
	if run.Execute && step.Plugin != nil {
		pl := step.Plugin
		zap.L().Debug("plugin step starting",
			zap.Int("step_index", stepIdx),
			zap.String("plugin_id", pl.ID),
			zap.String("action", pl.Action),
			zap.String("host_name", target.Name),
		)
		var totalAttempts int
		outcome := runHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
			inner := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
			out, execErr := pluginMgr.ExecuteStep(callCtx, step.Plugin.ID, step.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, run.Execute, secretsDry, kvSess)
			if execErr != nil {
				inner.ErrMsg = execErr.Error()
				if metrics.ObserverEnabled(obs) {
					obs.ObservePluginExec(pl.ID, pl.Action, "error", -1)
				}
				return inner
			}
			inner.Success = out.Success
			inner.Skipped = out.Skipped
			inner.ExitCode = out.ExitCode
			inner.Output = strings.TrimSpace(out.Stdout)
			if out.Stderr != "" {
				if inner.Output != "" {
					inner.Output += "\n"
				}
				inner.Output += strings.TrimSpace(out.Stderr)
			}
			if out.Err != "" {
				inner.ErrMsg = out.Err
			}
			if !out.Success && inner.ErrMsg == "" {
				inner.ErrMsg = "plugin step failed"
			}
			if metrics.ObserverEnabled(obs) {
				obs.ObservePluginExec(pl.ID, pl.Action, pluginExecStatus(inner.Success, inner.Skipped), -1)
			}
			return inner
		})
		res = outcome.Result
		totalAttempts = outcome.Attempts
		recordMaxAttempts(attemptMax, totalAttempts)
		if metrics.ObserverEnabled(obs) {
			obs.ObservePluginExecDuration(pl.ID, pl.Action, outcome.LastAttemptDuration)
		}
		zap.L().Debug("plugin step finished",
			zap.Int("step_index", stepIdx),
			zap.String("plugin_id", pl.ID),
			zap.String("host_name", target.Name),
			zap.Bool("success", res.Success),
			zap.Bool("skipped", res.Skipped),
			zap.String("err", res.ErrMsg),
		)
		runCueStepHooks(ctx, run, stepIdx, kind, step, target, &res, true)
		return res
	}
	out, err := pluginMgr.ExecuteStep(callCtx, step.Plugin.ID, step.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, run.Execute, secretsDry, kvSess)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	res.Success = out.Success
	res.Skipped = out.Skipped
	res.ExitCode = out.ExitCode
	res.Output = strings.TrimSpace(out.Stdout)
	if out.Stderr != "" {
		if res.Output != "" {
			res.Output += "\n"
		}
		res.Output += strings.TrimSpace(out.Stderr)
	}
	if out.Err != "" {
		res.ErrMsg = out.Err
	}
	if !out.Success && res.ErrMsg == "" {
		res.ErrMsg = "plugin step failed"
	}
	return res
}

func runCueStepPluginDry(out io.Writer, recipe cuetry.Recipe, recipeDir string, cliEnv map[string]string, sshUser string, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	pl := step.Plugin
	if pl == nil {
		return fmt.Errorf("step %d: internal plugin step", i)
	}
	WriteCueStepNotifyDryLine(out, step)
	WriteCueKVTunnelDryLine(out, recipe, i, step, recipe.Defaults)
	WriteCueSSHPrivateKeyDryLine(out, i, step, recipe.Defaults)
	WriteCueStepHooksDryLines(out, i, step)
	WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step, recipe.Defaults))
	if pluginMgr == nil || !pluginMgr.Enabled() {
		for _, target := range targets {
			_, _ = fmt.Fprintf(out, "step %d: kind=plugin name=%q %s plugin=%s action=%s (plugins disabled)\n",
				i, target.Name, FormatTargetForDryRun(target), pl.ID, pl.Action)
		}
		return nil
	}
	dryRun := &cueRun{CueRecipeRunParams: CueRecipeRunParams{
		Recipe:         recipe,
		RecipeDir:      recipeDir,
		CLIEnv:         cliEnv,
		SSHUser:        sshUser,
		SecretResolver: secretResolver,
		PluginMgr:      pluginMgr,
		Execute:        false,
	}}
	for _, target := range targets {
		res := runCuePluginOnHost(context.Background(), dryRun, i, cuetry.StepKindPlugin, step, target, cuetry.RecipeStepRetry{}, nil)
		_, _ = fmt.Fprintf(out, "step %d: kind=plugin name=%q %s plugin=%s action=%s success=%v skipped=%v output=%q\n",
			i, target.Name, FormatTargetForDryRun(target), pl.ID, pl.Action, res.Success, res.Skipped, strings.TrimSpace(res.Output))
		if res.ErrMsg != "" {
			_, _ = fmt.Fprintf(out, "  err=%q\n", res.ErrMsg)
		}
	}
	return nil
}
