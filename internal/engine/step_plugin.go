package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/stepkv"
	"go.uber.org/zap"
)

// StreamCueStepPlugin ...
func init() {
	RegisterStepExecutor(cuetry.KindPlugin, &PluginExecutor{})
}

// PluginExecutor executes the corresponding recipe step.
type PluginExecutor struct{}

// ExecuteStream streams the step execution.
func (e *PluginExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	stepIdx, kind, step, targets, ch, retryCfg, attemptMax := req.Index, req.Kind, req.Step, req.Targets, resCh, req.RetryCfg, req.AttemptMax
	if opts.PluginMgr == nil || !opts.PluginMgr.Enabled() {
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
	maxConc := RecipeHostMaxConc(step, opts.Recipe.Defaults)
	DispatchHostResults(ctx, targets, maxConc, 8, func(target TargetContext) HostExecResult {
		return runCuePluginOnHost(ctx, opts, stepIdx, kind, step, target, retryCfg, attemptMax)
	}, func(res HostExecResult) {
		ch <- res
	})
	return nil
}

func runCuePluginOnHost(ctx context.Context, opts ExecutionOptions, stepIdx int, kind string, step cuetry.Step, tc TargetContext, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) HostExecResult {
	target := tc.Record
	pluginMgr := opts.PluginMgr
	obs := opts.Obs
	res := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
	hostJSON, err := json.Marshal(target)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	secretsDry := !opts.Execute
	pls, _ := step.(*cuetry.PluginStep)
	if pls == nil || pls.Plugin == nil {
		res.ErrMsg = "internal: plugin step missing plugin field"
		return res
	}
	env := tc.Env
	var kvSess *stepkv.Session
	if opts.Execute && opts.RecipeKV != nil {
		kvSess, err = opts.RecipeKV.EnsureSession()
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
	pluginConfig, err := RewritePluginConfigTunnelStep(expanded, pls.Plugin.ID, opts.TunnelCoord, opts.SSHUser, target, opts.Execute)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	runAs := cuetry.EffectiveRunAs(step.Base(), opts.Recipe.Defaults)
	bridge := NewRemoteBridge(opts.SSHUser, target, opts.Cache, opts.Reg, opts.RecipeDir, runAs, env, pluginMgr.EffectivePaths(pls.Plugin.ID))
	hostCtx := &plugins.HostRunContext{
		SSHUser:              opts.SSHUser,
		Record:               target,
		RecipeDir:            opts.RecipeDir,
		Execute:              opts.Execute,
		SecretsDry:           secretsDry,
		RunAs:                runAs,
		Env:                  env,
		Bridge:               bridge,
		AllowedPaths:         pluginMgr.EffectivePaths(pls.Plugin.ID),
		RecipeSecrets:        MergeRecipeSecretRefs(opts.Recipe.Defaults, step),
		PluginID:             pls.Plugin.ID,
		MaxPostgresTimeoutMS: pluginMgr.TimeoutMS(),
	}
	if opts.TunnelCoord != nil {
		hostCtx.TunnelCoord = opts.TunnelCoord
	}
	if opts.SecretResolver != nil {
		hostCtx.ResolveSecret = opts.SecretResolver.Resolve
	}
	hostCtx.Postgres = NewPostgresBridge(hostCtx, opts.Pools)
	callCtx := plugins.WithHostRunContext(ctx, hostCtx)
	if kvSess != nil {
		callCtx = plugins.WithKVSession(callCtx, kvSess)
	}
	if opts.Execute && pls.Plugin != nil {
		pl := pls.Plugin
		zap.L().Debug(
			"plugin step starting",
			zap.Int("step_index", stepIdx),
			zap.String("plugin_id", pl.ID),
			zap.String("action", pl.Action),
			zap.String("host_name", target.Name),
		)
		var totalAttempts int
		outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
			inner := HostExecResult{Name: target.Name, IP: target.PrimaryIP, Provider: target.Provider, Success: false}
			out, execErr := pluginMgr.ExecuteStep(callCtx, pls.Plugin.ID, pls.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, opts.Execute, secretsDry, kvSess)
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
		zap.L().Debug(
			"plugin step finished",
			zap.Int("step_index", stepIdx),
			zap.String("plugin_id", pl.ID),
			zap.String("host_name", target.Name),
			zap.Bool("success", res.Success),
			zap.Bool("skipped", res.Skipped),
			zap.String("err", res.ErrMsg),
		)
		RunCueStepHooks(ctx, opts, stepIdx, kind, step, target, tc, &res, true)
		return res
	}
	out, err := pluginMgr.ExecuteStep(callCtx, pls.Plugin.ID, pls.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, opts.Execute, secretsDry, kvSess)
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

// ExecuteDryRun executes a dry run of the step.
func (e *PluginExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error {
	out, recipe, pluginMgr, i, step, targets := out, opts.Recipe, opts.PluginMgr, req.Index, req.Step, req.Targets
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
				i, target.Record.Name, FormatTargetForDryRun(target.Record), pl.ID, pl.Action)
		}
		return nil
	}
	dryOpts := opts
	dryOpts.Execute = false
	for _, target := range targets {
		res := runCuePluginOnHost(context.Background(), dryOpts, i, cuetry.KindPlugin, step, target, cuetry.RecipeStepRetry{}, nil)
		_, _ = fmt.Fprintf(out, "step %d: kind=plugin name=%q %s plugin=%s action=%s success=%v skipped=%v output=%q\n",
			i, target.Record.Name, FormatTargetForDryRun(target.Record), pl.ID, pl.Action, res.Success, res.Skipped, strings.TrimSpace(res.Output))
		if res.ErrMsg != "" {
			_, _ = fmt.Fprintf(out, "  err=%q\n", res.ErrMsg)
		}
	}
	return nil
}
