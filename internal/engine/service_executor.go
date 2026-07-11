package engine

import (
	"context"
	"fmt"
	"io"

	"github.com/shareed2k/honey/internal/cuetry"
)

func init() {
	RegisterStepExecutor(cuetry.KindService, &ServiceExecutor{})
}

// ServiceExecutor executes the corresponding recipe step.
type ServiceExecutor struct{}

// ExecuteStream streams the step execution.
func (e *ServiceExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	stepIdx, kind, step, targets, ch, retryCfg, attemptMax := req.Index, req.Kind, req.Step, req.Targets, resCh, req.RetryCfg, req.AttemptMax
	ss, ok := step.(*cuetry.ServiceStep)
	if !ok || ss.Service == nil {
		return fmt.Errorf("internal: service step missing service field")
	}

	b := step.Base()
	runAs := cuetry.EffectiveRunAs(b, opts.Recipe.Defaults)
	kvTunnel := cuetry.KVTunnelEnabled(step, opts.Recipe.Defaults)

	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		name := ShellSingleQuoted(ss.Service.Name)
		var script string

		action := ss.Service.State
		switch action {
		case "started":
			action = "start"
		case "stopped":
			action = "stop"
		case "restarted":
			action = "restart"
		case "reloaded":
			action = "reload"
		case "status":
			action = "status"
		}

		script = fmt.Sprintf(`
if command -v systemctl >/dev/null 2>&1; then
	systemctl %s %s
elif command -v service >/dev/null 2>&1; then
	service %s %s
else
	echo "Unsupported service manager" >&2
	exit 1
fi
`, action, name, name, action)

		if ss.Service.Enabled != nil && action != "status" {
			enableAction := "enable"
			if !*ss.Service.Enabled {
				enableAction = "disable"
			}
			script += fmt.Sprintf(`
if command -v systemctl >/dev/null 2>&1; then
	systemctl %s %s
fi
`, enableAction, name)
		}

		remoteCmd, err := cuetry.WrapRemoteShell(runAs, script)
		if err != nil {
			return "echo " + ShellSingleQuoted("wrap err: "+err.Error())
		}
		return remoteCmd
	}

	post := CueRecipeSSHPostHostResult(ctx, opts, stepIdx, kind, step, kvTunnel)
	return StreamSSHParallel(ctx, opts.SSHUser, targets, kvTunnel, cmdFunc, ch, BatchOptions{
		MaxConc:        RecipeHostMaxConc(step, opts.Recipe.Defaults),
		Cache:          opts.Cache,
		RecipeKV:       opts.RecipeKV,
		RecipeScopedKV: kvTunnel,
		Post:           post,
		RetryCfg:       retryCfg,
		Obs:            opts.Obs,
		AttemptMax:     attemptMax,
		Reg:            opts.Reg,
		CmdTimeout:     opts.CmdTimeout,
	})
}

// ExecuteDryRun performs a dry run of the step.
func (e *ServiceExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, _ ExecutionOptions, out io.Writer) error {
	ss, _ := req.Step.(*cuetry.ServiceStep)
	for _, target := range req.Targets {
		_, _ = fmt.Fprintf(out, "step %d: kind=service name=%q state=%q host=%q\n", req.Index, ss.Service.Name, ss.Service.State, target.Record.Name)
	}
	return nil
}
