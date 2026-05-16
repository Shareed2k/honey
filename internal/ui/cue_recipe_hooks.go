package ui

import (
	"bytes"
	"context"
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
)

const (
	cueLocalHookTimeout   = 60 * time.Second
	maxHookErrMsgEnvRunes = 512
	maxHookOutputRunes    = 8000
)

func cueRecipeSSHPostHostResult(_ context.Context, recipe cuetry.Recipe, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, recipeDir, sshUser string, cliEnv map[string]string, cache *ClientCache, recipeKV *RecipeKVCoordinator, recipeScopedKV bool, secretResolver cuetry.SecretResolver) SSHPostHostResultFunc {
	return func(hctx context.Context, r hosts.Record, res *HostExecResult) {
		runCueStepHooks(hctx, recipe, stepIdx, kind, step, r, res, recipeDir, sshUser, cliEnv, cache, recipeKV, recipeScopedKV, secretResolver)
	}
}

func runCueStepHooks(ctx context.Context, recipe cuetry.Recipe, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, r hosts.Record, res *HostExecResult, recipeDir, sshUser string, cliEnv map[string]string, cache *ClientCache, recipeKV *RecipeKVCoordinator, recipeScopedKV bool, secretResolver cuetry.SecretResolver) {
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
		runCueStepHookLocal(ctx, recipe, stepIdx+1, kind, phase, r, res, hook, recipeDir)
	case "remote":
		runCueStepHookRemote(ctx, recipe, stepIdx+1, kind, phase, step, r, res, hook, sshUser, cliEnv, cache, recipeKV, recipeScopedKV, secretResolver)
	default:
		res.HookPhase = ""
		zap.L().Warn("cue recipe hook: invalid where (should be caught at parse)", zap.String("where", where), zap.Int("step", stepIdx+1))
	}
}

func runCueStepHookRemote(ctx context.Context, recipe cuetry.Recipe, stepNo int, kind cuetry.StepKind, phase string, step cuetry.RecipeStep, r hosts.Record, stepRes *HostExecResult, hook *cuetry.RecipeStepHook, sshUser string, cliEnv map[string]string, cache *ClientCache, recipeKV *RecipeKVCoordinator, recipeScopedKV bool, secretResolver cuetry.SecretResolver) {
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	if rs := strings.TrimSpace(hook.RunAs); rs != "" {
		runAs = rs
	}
	kvTunnel := cuetry.KVTunnelEnabled(step, recipe.Defaults)
	build := func(r2 hosts.Record, kv map[string]string) string {
		env, err := cuetry.EffectiveEnvForRemoteHook(ctx, true, secretResolver, step, recipe.Defaults, hook, cliEnv, &r2)
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
	hres := runOneRemoteSSH(sshUser, r, cache, kvTunnel, build, recipeKV, recipeScopedKV)
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
		CueHookNotifyRemote(ctx, recipe, stepNo, kind, phase, r.Name, hook.Notify, body)
	}
}

func runCueStepHookLocal(ctx context.Context, recipe cuetry.Recipe, stepNo int, kind cuetry.StepKind, phase string, r hosts.Record, stepRes *HostExecResult, hook *cuetry.RecipeStepHook, recipeDir string) {
	hctx, cancel := context.WithTimeout(ctx, cueLocalHookTimeout)
	defer cancel()
	cmd := exec.CommandContext(hctx, "sh", "-c", strings.TrimSpace(hook.Command)) // #nosec G204 -- local hooks run arbitrary operator shell by design (cue-exec docs); trusted recipes only
	if d := strings.TrimSpace(recipeDir); d != "" {
		cmd.Dir = d
	}
	env, err := buildLocalHookEnv(recipe.Name, stepNo, phase, *stepRes, r, hook)
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
		CueHookNotifyRemote(ctx, recipe, stepNo, kind, phase, r.Name, hook.Notify, body)
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
