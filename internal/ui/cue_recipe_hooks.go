package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/stepkv"
)

const (
	cueLocalHookTimeout   = 60 * time.Second
	maxHookErrMsgEnvRunes = 512
	maxHookOutputRunes    = 8000
)

func cueRecipeSSHPostHostResult(_ context.Context, run *cueRun, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, recipeScopedKV bool) SSHPostHostResultFunc {
	return func(hctx context.Context, r hosts.Record, res *HostExecResult) {
		if step.CheckCmd != "" && strings.Contains(res.Output, "HONEY_CHECK_CMD_OK") {
			res.Changed = false
			res.Success = true
			res.ExitCode = 0
			res.Output = "Skipped: check_cmd passed"
		} else {
			res.Changed = true
		}
		runCueStepHooks(hctx, run, stepIdx, kind, step, r, res, recipeScopedKV)
	}
}

func runCueStepHooks(ctx context.Context, run *cueRun, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, r hosts.Record, res *HostExecResult, recipeScopedKV bool) {
	h := step.Hooks
	if h == nil {
		return
	}
	var hook *cuetry.RecipeStepHook
	phase := ""
	switch {
	case res.Success && h.OnSuccess != nil:
		hook = h.OnSuccess
		phase = "on_success"
	case !res.Success && h.OnFailure != nil:
		hook = h.OnFailure
		phase = "on_failure"
	default:
		return
	}
	where := strings.TrimSpace(hook.Where)
	res.HookPhase = phase
	switch where {
	case "local":
		runCueStepHookLocal(ctx, run, stepIdx+1, kind, phase, r, res, hook, recipeScopedKV)
	case "remote":
		runCueStepHookRemote(ctx, run, stepIdx+1, kind, phase, step, r, res, hook, recipeScopedKV)
	default:
		res.HookPhase = ""
		zap.L().Warn("cue recipe hook: invalid where (should be caught at parse)", zap.String("where", where), zap.Int("step", stepIdx+1))
	}
}

func runCueStepHookRemote(ctx context.Context, run *cueRun, stepNo int, kind cuetry.StepKind, phase string, step cuetry.RecipeStep, r hosts.Record, stepRes *HostExecResult, hook *cuetry.RecipeStepHook, recipeScopedKV bool) {
	runAs := cuetry.EffectiveRunAs(step, run.Recipe.Defaults)
	if rs := strings.TrimSpace(hook.RunAs); rs != "" {
		runAs = rs
	}
	kvTunnel := cuetry.KVTunnelEnabled(step, run.Recipe.Defaults)
	build := func(r2 hosts.Record, kv map[string]string) string {
		env, err := cuetry.EffectiveEnvForRemoteHook(ctx, true, run.SecretResolver, step, run.Recipe.Defaults, hook, run.CLIEnv, &r2)
		if err != nil {
			return fmt.Sprintf("echo 'env err: %s'", err.Error())
		}
		for k, v := range kv {
			env[k] = v
		}
		inner, err := cuetry.ShellExportPrefixForRemote(env, strings.TrimSpace(hook.Command))
		if err != nil {
			return fmt.Sprintf("echo 'export err: %s'", err.Error())
		}
		remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
		if err != nil {
			return fmt.Sprintf("echo 'wrap err: %s'", err.Error())
		}
		return remoteCmd
	}
	_ = ctx // remote path uses existing pooled sessions; cancellation is best-effort via process on remote
	hres := runOneRemoteSSH(run.SSHUser, r, run.cache, kvTunnel, build, run.recipeKV, recipeScopedKV)
	var b strings.Builder
	if strings.TrimSpace(hres.Output) != "" {
		b.WriteString(strings.TrimSpace(hres.Output))
	}
	if hres.ErrMsg != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		_, _ = fmt.Fprintf(&b, "(hook exit %d: %s)", hres.ExitCode, hres.ErrMsg)
	}
	stepRes.HookOutput = truncateRunes(strings.TrimSpace(b.String()), maxHookOutputRunes)
	if !hres.Success {
		zap.L().Warn("cue recipe hook: remote hook command failed (original step outcome unchanged)",
			zap.Int("step", stepNo), zap.String("phase", phase), zap.String("host", r.Name),
			zap.Int("exit", hres.ExitCode), zap.String("err", hres.ErrMsg))
	}
	if hook.Notify != nil {
		body := formatHookNotifyBody(phase, r, hres)
		CueHookNotifyRemote(ctx, run.Recipe, stepNo, kind, phase, r.Name, hook.Notify, body)
	}
}

func runCueStepHookLocal(ctx context.Context, run *cueRun, stepNo int, kind cuetry.StepKind, phase string, r hosts.Record, stepRes *HostExecResult, hook *cuetry.RecipeStepHook, recipeScopedKV bool) {
	if hook.Plugin != nil {
		runCueStepHookLocalPlugin(ctx, run, stepNo, kind, phase, r, stepRes, hook, recipeScopedKV)
		return
	}
	hctx, cancel := context.WithTimeout(ctx, cueLocalHookTimeout)
	defer cancel()
	cmd := exec.CommandContext(hctx, "sh", "-c", strings.TrimSpace(hook.Command)) // #nosec G204 -- local hooks run arbitrary operator shell by design (cue-exec docs); trusted recipes only
	if d := strings.TrimSpace(run.RecipeDir); d != "" {
		cmd.Dir = d
	}
	env, err := buildLocalHookEnv(run.Recipe.Name, stepNo, phase, *stepRes, r, hook)
	if err != nil {
		stepRes.HookOutput = truncateRunes(fmt.Sprintf("hook env: %v", err), maxHookOutputRunes)
		zap.L().Warn("cue recipe hook: local env build failed",
			zap.Int("step", stepNo), zap.String("phase", phase), zap.String("host", r.Name), zap.Error(err))
		return
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	outStr := strings.TrimSpace(stdout.String())
	errStr := strings.TrimSpace(stderr.String())
	combined := strings.TrimSpace(strings.TrimSpace(outStr + "\n" + errStr))
	stepRes.HookOutput = truncateRunes(combined, maxHookOutputRunes)
	exitCode := 0
	success := runErr == nil
	if runErr != nil {
		success = false
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
		zap.L().Warn("cue recipe hook: local hook command failed (original step outcome unchanged)",
			zap.Int("step", stepNo), zap.String("phase", phase), zap.String("host", r.Name),
			zap.Int("exit", exitCode), zap.Error(runErr))
	}
	if hook.Notify != nil {
		pseudo := HostExecResult{
			Name:     r.Name,
			IP:       r.PrimaryIP,
			Provider: r.Provider,
			Success:  success,
			ExitCode: exitCode,
			Output:   combined,
			ErrMsg:   runErrString(runErr),
		}
		body := formatHookNotifyBody(phase, r, pseudo)
		CueHookNotifyRemote(ctx, run.Recipe, stepNo, kind, phase, r.Name, hook.Notify, body)
	}
}

func runErrString(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	return truncateRunes(s, maxHookErrMsgEnvRunes)
}

func buildLocalHookEnv(recipeName string, stepNo int, phase string, stepRes HostExecResult, r hosts.Record, hook *cuetry.RecipeStepHook) ([]string, error) {
	base := os.Environ()
	m := make(map[string]string, len(base)+32)
	for _, kv := range base {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		m[k] = kv[i+1:]
	}
	if len(hook.Env) > 0 {
		if err := cuetry.ValidateRecipeEnvMap(hook.Env); err != nil {
			return nil, err
		}
		for k, v := range hook.Env {
			m[k] = v
		}
	}
	m["HONEY_HOST_NAME"] = r.Name
	m["HONEY_HOST_PRIMARY_IP"] = r.PrimaryIP
	m["HONEY_HOST_PROVIDER"] = r.Provider
	if r.Zone != "" {
		m["HONEY_HOST_ZONE"] = r.Zone
	}
	if r.Region != "" {
		m["HONEY_HOST_REGION"] = r.Region
	}
	m["HONEY_HOOK_STEP"] = fmt.Sprintf("%d", stepNo)
	m["HONEY_HOOK_PHASE"] = phase
	m["HONEY_RECIPE_NAME"] = strings.TrimSpace(recipeName)
	if stepRes.Success {
		m["HONEY_HOST_STEP_SUCCESS"] = "true"
	} else {
		m["HONEY_HOST_STEP_SUCCESS"] = "false"
	}
	m["HONEY_HOST_EXIT_CODE"] = fmt.Sprintf("%d", stepRes.ExitCode)
	m["HONEY_HOST_ERR_MSG"] = truncateRunes(stepRes.ErrMsg, maxHookErrMsgEnvRunes)
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out, nil
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit]) + "…"
}

func formatHookNotifyBody(phase string, r hosts.Record, hres HostExecResult) string {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "hook phase=%s host=%q ip=%q provider=%q success=%v exit=%d err=%q\n",
		phase, r.Name, r.PrimaryIP, r.Provider, hres.Success, hres.ExitCode, hres.ErrMsg)
	if strings.TrimSpace(hres.Output) != "" {
		b.WriteString(strings.TrimSpace(hres.Output))
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// WriteCueStepHooksDryLines prints one plan line per configured hook (no secrets).
func WriteCueStepHooksDryLines(out io.Writer, stepIdx int, step cuetry.RecipeStep) {
	if step.Hooks == nil {
		return
	}
	write := func(phase string, hook *cuetry.RecipeStepHook) {
		if hook == nil {
			return
		}
		preview := hookCommandPreview(hook.Command)
		_, _ = fmt.Fprintf(out, "  step %d hook %s: where=%s command_preview=%q\n", stepIdx, phase, strings.TrimSpace(hook.Where), preview)
	}
	write("on_success", step.Hooks.OnSuccess)
	write("on_failure", step.Hooks.OnFailure)
}

func hookCommandPreview(cmd string) string {
	s := strings.TrimSpace(cmd)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}

func runCueStepHookLocalPlugin(ctx context.Context, run *cueRun, stepNo int, _ cuetry.StepKind, phase string, r hosts.Record, stepRes *HostExecResult, hook *cuetry.RecipeStepHook, recipeScopedKV bool) {
	pluginMgr := run.PluginMgr
	recipeKV := run.recipeKV
	recipe := run.Recipe
	if pluginMgr == nil || !pluginMgr.Enabled() {
		stepRes.HookOutput = truncateRunes("hook plugin: plugins disabled", maxHookOutputRunes)
		return
	}
	pl := hook.Plugin
	hostJSON, err := json.Marshal(r)
	if err != nil {
		stepRes.HookOutput = truncateRunes("hook plugin: "+err.Error(), maxHookOutputRunes)
		return
	}
	resJSON, err := json.Marshal(stepRes)
	if err != nil {
		stepRes.HookOutput = truncateRunes("hook plugin: "+err.Error(), maxHookOutputRunes)
		return
	}
	env, err := buildLocalHookEnv(recipe.Name, stepNo, phase, *stepRes, r, hook)
	if err != nil {
		stepRes.HookOutput = truncateRunes(fmt.Sprintf("hook env: %v", err), maxHookOutputRunes)
		return
	}
	envMap := make(map[string]string)
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i > 0 {
			envMap[kv[:i]] = kv[i+1:]
		}
	}
	var kvSess *stepkv.Session
	if recipeScopedKV && recipeKV != nil {
		var kvErr error
		kvSess, kvErr = recipeKV.EnsureSession()
		if kvErr != nil {
			stepRes.HookOutput = truncateRunes("hook plugin kv: "+kvErr.Error(), maxHookOutputRunes)
			return
		}
	}
	hctx, cancel := context.WithTimeout(ctx, cueLocalHookTimeout)
	defer cancel()
	in := apiv1.OnStepResultInput{
		RecipeName: recipe.Name,
		StepIndex:  stepNo - 1,
		Phase:      phase,
		Host:       hostJSON,
		Result:     resJSON,
		Env:        envMap,
	}
	out, err := pluginMgr.OnStepResult(hctx, pl.ID, pl.Action, pl.Config, in, kvSess)
	if err != nil {
		stepRes.HookOutput = truncateRunes(err.Error(), maxHookOutputRunes)
		return
	}
	stepRes.HookOutput = truncateRunes(strings.TrimSpace(out.Output), maxHookOutputRunes)
	if out.Err != "" {
		zap.L().Warn("cue recipe hook: plugin hook returned error",
			zap.Int("step", stepNo), zap.String("phase", phase), zap.String("plugin", pl.ID), zap.String("err", out.Err))
	}
}
