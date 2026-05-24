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

func streamCueStepPlugin(ctx context.Context, recipe cuetry.Recipe, recipeDir string, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, cliEnv map[string]string, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, pluginMgr *plugins.Manager, secretResolver cuetry.SecretResolver, execute bool, cache *ClientCache, recipeKV *RecipeKVCoordinator, tunnelCoord *RecipeTunnelCoordinator, outputStore *cuetry.StepOutputStore, outputCapture *cuetry.RecipeOutputCapture, retryCfg cuetry.RecipeStepRetry, obs metrics.Observer, attemptMax *atomic.Int32) error {
	if pluginMgr == nil || !pluginMgr.Enabled() {
		return fmt.Errorf("plugin step requires plugins.enabled in honey config")
	}
	pl := step.Plugin
	if pl == nil {
		return fmt.Errorf("internal plugin step")
	}
	maxConc := recipeHostMaxConc(step, recipe.Defaults)
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
			res := runCuePluginOnHost(ctx, recipe, recipeDir, stepIdx, kind, step, target, cliEnv, sshUser, pluginMgr, secretResolver, execute, cache, recipeKV, tunnelCoord, outputStore, outputCapture, retryCfg, obs, attemptMax)
			ch <- res
		}()
	}
	wg.Wait()
	return nil
}

func runCuePluginOnHost(ctx context.Context, recipe cuetry.Recipe, recipeDir string, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, target hosts.Record, cliEnv map[string]string, sshUser string, pluginMgr *plugins.Manager, secretResolver cuetry.SecretResolver, execute bool, cache *ClientCache, recipeKV *RecipeKVCoordinator, tunnelCoord *RecipeTunnelCoordinator, outputStore *cuetry.StepOutputStore, outputCapture *cuetry.RecipeOutputCapture, retryCfg cuetry.RecipeStepRetry, obs metrics.Observer, attemptMax *atomic.Int32) HostExecResult {
	res := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
	hostJSON, err := json.Marshal(target)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	secretsDry := !execute
	env, err := cuetry.EffectiveEnvForRunEx(ctx, execute, secretResolver, step, recipe.Defaults, cliEnv, &target, cueEnvRunOpts(&recipe, outputStore, outputCapture, kvReaderFromCoordinator(recipeKV), secretsDry))
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	var kvSess *stepkv.Session
	if execute && recipeKV != nil {
		kvSess, err = recipeKV.EnsureSession()
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
	pluginConfig, err := RewritePluginConfigTunnelStep(expanded, step.Plugin.ID, tunnelCoord, sshUser, target, execute)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	bridge := NewPluginRemoteBridge(sshUser, target, cache, recipeDir, runAs, env, pluginMgr.EffectivePaths(step.Plugin.ID))
	hostCtx := &plugins.HostRunContext{
		SSHUser:              sshUser,
		Record:               target,
		RecipeDir:            recipeDir,
		Execute:              execute,
		SecretsDry:           secretsDry,
		RunAs:                runAs,
		Env:                  env,
		Bridge:               bridge,
		AllowedPaths:         pluginMgr.EffectivePaths(step.Plugin.ID),
		RecipeSecrets:        mergeRecipeSecretRefs(recipe.Defaults, step),
		PluginID:             step.Plugin.ID,
		MaxPostgresTimeoutMS: pluginMgr.TimeoutMS(),
	}
	if tunnelCoord != nil {
		hostCtx.TunnelCoord = tunnelCoord
	}
	if secretResolver != nil {
		hostCtx.ResolveSecret = secretResolver.Resolve
	}
	hostCtx.Postgres = NewPluginPostgresBridge(hostCtx)
	callCtx := plugins.WithHostRunContext(ctx, hostCtx)
	if kvSess != nil {
		callCtx = plugins.WithKVSession(callCtx, kvSess)
	}
	if execute && step.Plugin != nil {
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
			out, execErr := pluginMgr.ExecuteStep(callCtx, step.Plugin.ID, step.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, execute, secretsDry, kvSess)
			if execErr != nil {
				inner.ErrMsg = execErr.Error()
				if obs != nil {
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
			if obs != nil {
				obs.ObservePluginExec(pl.ID, pl.Action, pluginExecStatus(inner.Success, inner.Skipped), -1)
			}
			return inner
		})
		res = outcome.Result
		totalAttempts = outcome.Attempts
		recordMaxAttempts(attemptMax, totalAttempts)
		if obs != nil {
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
		runCueStepHooks(ctx, recipe, stepIdx, kind, step, target, &res, recipeDir, sshUser, cliEnv, cache, recipeKV, true, secretResolver, pluginMgr)
		return res
	}
	out, err := pluginMgr.ExecuteStep(callCtx, step.Plugin.ID, step.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, execute, secretsDry, kvSess)
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
	for _, target := range targets {
		res := runCuePluginOnHost(context.Background(), recipe, recipeDir, i, cuetry.StepKindPlugin, step, target, cliEnv, sshUser, pluginMgr, secretResolver, false, nil, nil, nil, nil, nil, cuetry.RecipeStepRetry{}, nil, nil)
		_, _ = fmt.Fprintf(out, "step %d: kind=plugin name=%q %s plugin=%s action=%s success=%v skipped=%v output=%q\n",
			i, target.Name, FormatTargetForDryRun(target), pl.ID, pl.Action, res.Success, res.Skipped, strings.TrimSpace(res.Output))
		if res.ErrMsg != "" {
			_, _ = fmt.Fprintf(out, "  err=%q\n", res.ErrMsg)
		}
	}
	return nil
}
