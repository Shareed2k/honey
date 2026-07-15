package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
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
	sshUser := opts.SSHUser
	if u := strings.TrimSpace(target.Meta["ssh_user"]); u != "" {
		sshUser = u
	}
	// A runtime:docker plugin targeting a real remote host runs its
	// shim-container on that host's Docker daemon (over SSH) instead of the
	// operator's local daemon. Only on --execute (dry-run keeps everything
	// local) and only when a per-run session exists to own the remote
	// container's lifecycle. host: "_" / localhost stays on the local path.
	useRemoteDocker := opts.Execute &&
		opts.DockerPluginSess != nil &&
		pluginMgr.IsDockerPlugin(pls.Plugin.ID) &&
		isRemoteHostRecord(target)
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
			var (
				out     apiv1.ExecuteStepOutput
				execErr error
			)
			if useRemoteDocker {
				factory := newDockerPluginSSHBackendFactory(ctx, opts.Cache, sshUser, runAs, target)
				out, execErr = opts.DockerPluginSess.ExecuteStep(callCtx, factory, dockerPluginHostKey(target), pls.Plugin.ID, pls.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, opts.Execute, secretsDry, kvSess)
			} else {
				out, execErr = pluginMgr.ExecuteStep(callCtx, pls.Plugin.ID, pls.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, opts.Execute, secretsDry, kvSess)
			}
			if execErr != nil {
				inner.ErrMsg = execErr.Error()
				if metrics.ObserverEnabled(obs) {
					obs.ObservePluginExec(pl.ID, pl.Action, "error", -1)
				}
				return inner
			}
			if kvErr := writePluginKVResult(kvSess, pl, target.Name, out.Stdout); kvErr != nil {
				inner.ErrMsg = kvErr.Error()
				if metrics.ObserverEnabled(obs) {
					obs.ObservePluginExec(pl.ID, pl.Action, "error", -1)
				}
				return inner
			}
			applyExecuteStepOutput(&inner, out)
			inner.KVCaptureKey = strings.TrimSpace(pl.KVKey)
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
	// kvSess is always nil below this point: it's only built when opts.Execute
	// is true (line 67 above), and the opts.Execute branch always returns above
	// (line 175) — so plugin.kv_key writes only ever happen on a real --execute
	// run, never here on the dry-run/no-op fallthrough path.
	out, err := pluginMgr.ExecuteStep(callCtx, pls.Plugin.ID, pls.Plugin.Action, pluginConfig, stepIdx, hostJSON, env, opts.Execute, secretsDry, kvSess)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	applyExecuteStepOutput(&res, out)
	return res
}

// applyExecuteStepOutput copies a plugin action's result onto a HostExecResult:
// stdout+stderr concatenated into Output, and ErrMsg from out.Err (or a
// default message on failure) if the plugin didn't already report one.
func applyExecuteStepOutput(res *HostExecResult, out apiv1.ExecuteStepOutput) {
	res.Success = out.Success
	res.Skipped = out.Skipped
	res.ExitCode = out.ExitCode
	res.Stdout = strings.TrimSpace(out.Stdout)
	res.Output = res.Stdout
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
}

// writePluginKVResult stores a plugin action's raw stdout in the recipe KV
// store when the step declares plugin.kv_key — the escape hatch for output
// too large for env_from's 8KB cap (stepkv.Put allows up to 64KB, rejecting
// rather than truncating oversized values). Writes regardless of the
// action's own exit_code/Err, since the whole point is capturing whatever
// the plugin produced (error payload included) for a later step to inspect.
// A no-op when kv_key is unset or kvSess is nil (dry-run).
func writePluginKVResult(kvSess *stepkv.Session, pl *cuetry.RecipeStepPlugin, hostName, stdout string) error {
	if kvSess == nil || pl == nil || strings.TrimSpace(pl.KVKey) == "" {
		return nil
	}
	key, err := cuetry.ResolveStepKVBaseKey(pl.KVKey, pl.KVKeyPerHost, hostName)
	if err != nil {
		return fmt.Errorf("plugin kv_key %q: %w", pl.KVKey, err)
	}
	if err := kvSess.Put(key, stdout); err != nil {
		if errors.Is(err, stepkv.ErrValueTooLong) {
			return fmt.Errorf("plugin kv_key %q: value exceeds max kv size", pl.KVKey)
		}
		return fmt.Errorf("plugin kv_key %q: %w", pl.KVKey, err)
	}
	return nil
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
