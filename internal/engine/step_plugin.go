package engine

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

// StreamCueStepPlugin ...
func StreamCueStepPlugin(ctx context.Context, run *CueRun, stepIdx int, kind string, step cuetry.Step, targets []hosts.Record, ch chan<- HostExecResult, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) error {
	if run.Params.PluginMgr == nil || !run.Params.PluginMgr.Enabled() {
		return fmt.Errorf("plugin step requires plugins.enabled in honey config")
	}
	pls, _ := step.(*cuetry.PluginStep)
	pl := (*cuetry.RecipeStepPlugin)(nil)
	if pls != nil {
		pl = pls.Plugin
	}
	if pl == nil {
		return fmt.Errorf("internal plugin step")
	}
	maxConc := RecipeHostMaxConc(step, run.Params.Recipe.Defaults)
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

func runCuePluginOnHost(ctx context.Context, run *CueRun, stepIdx int, kind string, step cuetry.Step, target hosts.Record, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) HostExecResult {
	pluginMgr := run.Params.PluginMgr
	obs := run.Params.Obs
	res := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
	hostJSON, err := json.Marshal(target)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	secretsDry := !run.Params.Execute
	pls, _ := step.(*cuetry.PluginStep)
	if pls == nil || pls.Plugin == nil {
		res.ErrMsg = "internal: plugin step missing plugin field"
		return res
	}
	env, err := cuetry.EffectiveEnvForRunEx(ctx, run.Params.Execute, run.Params.SecretResolver, step.Base(), run.Params.Recipe.Defaults, run.Params.CLIEnv, &target, CueEnvRunOpts(&run.Params.Recipe, run.OutputStore, run.OutputCapture, KvReaderFromCoordinator(run.RecipeKV), secretsDry))
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	var kvSess *stepkv.Session
	if run.Params.Execute && run.RecipeKV != nil {
		kvSess, err = run.RecipeKV.EnsureSession()
		if err != nil {
			res.ErrMsg = err.Error()
			return res
		}
	}
	expanded, err := cuetry.ExpandPluginConfigJSON(pls.Plugin.Config, env, secretsDry)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	pluginConfig, err := RewritePluginConfigTunnelStep(expanded, pls.Plugin.ID, run.TunnelCoord, run.Params.SSHUser, target, run.Params.Execute)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	runAs := cuetry.EffectiveRunAs(step.Base(), run.Params.Recipe.Defaults)
	bridge := NewRemoteBridge(run.Params.SSHUser, target, run.Cache, run.Params.Reg, run.Params.RecipeDir, runAs, env, pluginMgr.EffectivePaths(pls.Plugin.ID))
	hostCtx := &plugins.HostRunContext{
		SSHUser:              run.Params.SSHUser,
		Record:               target,
		RecipeDir:            run.Params.RecipeDir,
		Execute:              run.Params.Execute,
		SecretsDry:           secretsDry,
		RunAs:                runAs,
		Env:                  env,
		Bridge:               bridge,
		AllowedPaths:         pluginMgr.EffectivePaths(pls.Plugin.ID),
		RecipeSecrets:        MergeRecipeSecretRefs(run.Params.Recipe.Defaults, step),
		PluginID:             pls.Plugin.ID,
		MaxPostgresTimeoutMS: pluginMgr.TimeoutMS(),
	}
	if run.TunnelCoord != nil {
		hostCtx.TunnelCoord = run.TunnelCoord
	}
	if run.Params.SecretResolver != nil {
		hostCtx.ResolveSecret = run.Params.SecretResolver.Resolve
	}
	hostCtx.Postgres = NewPostgresBridge(hostCtx, run.Params.Pools)
	callCtx := plugins.WithHostRunContext(ctx, hostCtx)
	if kvSess != nil {
		callCtx = plugins.WithKVSession(callCtx, kvSess)
	}
	if run.Params.Execute && pls.Plugin != nil {
		pl := pls.Plugin
		zap.L().Debug("plugin step starting",
			zap.Int("step_index", stepIdx),
			zap.String("plugin_id", pl.ID),
			zap.String("action", pl.Action),
			zap.String("host_name", target.Name),
		)
		var totalAttempts int
		outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
			inner := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
			out, execErr := pluginMgr.ExecuteStep(callCtx, pls.Plugin.ID, pls.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, run.Params.Execute, secretsDry, kvSess)
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
				obs.ObservePluginExec(pl.ID, pl.Action, PluginExecStatus(inner.Success, inner.Skipped), -1)
			}
			return inner
		})
		res = outcome.Result
		totalAttempts = outcome.Attempts
		RecordMaxAttempts(attemptMax, totalAttempts)
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
		RunCueStepHooks(ctx, run, stepIdx, kind, step, target, &res, true)
		return res
	}
	out, err := pluginMgr.ExecuteStep(callCtx, pls.Plugin.ID, pls.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, run.Params.Execute, secretsDry, kvSess)
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

// RunCueStepPluginDry ...
func RunCueStepPluginDry(out io.Writer, recipe cuetry.Recipe, recipeDir string, cliEnv map[string]string, sshUser string, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager, i int, step cuetry.Step, targets []hosts.Record) error {
	pls, _ := step.(*cuetry.PluginStep)
	pl := (*cuetry.RecipeStepPlugin)(nil)
	if pls != nil {
		pl = pls.Plugin
	}
	if pl == nil {
		return fmt.Errorf("step %d: internal plugin step", i)
	}
	WriteCueStepNotifyDryLine(out, step)
	WriteCueKVTunnelDryLine(out, recipe, i, step, recipe.Defaults)
	WriteCueSSHPrivateKeyDryLine(out, i, step, recipe.Defaults)
	WriteCueStepHooksDryLines(out, i, step)
	WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step.Base(), recipe.Defaults))
	if pluginMgr == nil || !pluginMgr.Enabled() {
		for _, target := range targets {
			_, _ = fmt.Fprintf(out, "step %d: kind=plugin name=%q %s plugin=%s action=%s (plugins disabled)\n",
				i, target.Name, FormatTargetForDryRun(target), pl.ID, pl.Action)
		}
		return nil
	}
	dryRun := &CueRun{Params: CueRecipeRunParams{
		Recipe:         recipe,
		RecipeDir:      recipeDir,
		CLIEnv:         cliEnv,
		SSHUser:        sshUser,
		SecretResolver: secretResolver,
		PluginMgr:      pluginMgr,
		Execute:        false,
	}}
	for _, target := range targets {
		res := runCuePluginOnHost(context.Background(), dryRun, i, cuetry.KindPlugin, step, target, cuetry.RecipeStepRetry{}, nil)
		_, _ = fmt.Fprintf(out, "step %d: kind=plugin name=%q %s plugin=%s action=%s success=%v skipped=%v output=%q\n",
			i, target.Name, FormatTargetForDryRun(target), pl.ID, pl.Action, res.Success, res.Skipped, strings.TrimSpace(res.Output))
		if res.ErrMsg != "" {
			_, _ = fmt.Fprintf(out, "  err=%q\n", res.ErrMsg)
		}
	}
	return nil
}
