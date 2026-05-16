package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/stepkv"
)

func streamCueStepPlugin(ctx context.Context, recipe cuetry.Recipe, recipeDir string, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, cliEnv map[string]string, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, pluginMgr *plugins.Manager, secretResolver cuetry.SecretResolver, execute bool, cache *ClientCache, recipeKV *RecipeKVCoordinator) error {
	if pluginMgr == nil || !pluginMgr.Enabled() {
		return fmt.Errorf("plugin step requires plugins.enabled in honey config")
	}
	pl := step.Plugin
	if pl == nil {
		return fmt.Errorf("internal plugin step")
	}
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := runCuePluginOnHost(ctx, recipe, recipeDir, stepIdx, kind, step, target, cliEnv, sshUser, pluginMgr, secretResolver, execute, cache, recipeKV)
			ch <- res
		}()
	}
	wg.Wait()
	return nil
}

func runCuePluginOnHost(ctx context.Context, recipe cuetry.Recipe, recipeDir string, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, target hosts.Record, cliEnv map[string]string, sshUser string, pluginMgr *plugins.Manager, secretResolver cuetry.SecretResolver, execute bool, cache *ClientCache, recipeKV *RecipeKVCoordinator) HostExecResult {
	res := HostExecResult{Name: target.Name, Success: false}
	hostJSON, err := json.Marshal(target)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	env, err := cuetry.EffectiveEnvForRun(ctx, execute, secretResolver, step, recipe.Defaults, cliEnv, &target)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	if !execute {
		res.Success = true
		res.Output = fmt.Sprintf("dry-run plugin %s action=%s", step.Plugin.ID, step.Plugin.Action)
		return res
	}
	var kvSess *stepkv.Session
	if cuetry.KVTunnelEnabled(step, recipe.Defaults) && recipeKV != nil {
		kvSess, err = recipeKV.EnsureSession()
		if err != nil {
			res.ErrMsg = err.Error()
			return res
		}
	}
	out, err := pluginMgr.ExecuteStep(ctx, step.Plugin.ID, step.Plugin.Action, step.Plugin.Config, stepIdx, hostJSON, env, true, false, kvSess)
	if err != nil {
		res.ErrMsg = err.Error()
		return res
	}
	res.Success = out.Success
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
	recipeScoped := cuetry.KVTunnelEnabled(step, recipe.Defaults)
	runCueStepHooks(ctx, recipe, stepIdx, kind, step, target, &res, recipeDir, sshUser, cliEnv, cache, recipeKV, recipeScoped, secretResolver, pluginMgr)
	return res
}
