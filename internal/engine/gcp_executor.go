package engine

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
)

func init() {
	RegisterStepExecutor(cuetry.KindGcp, &GcpExecutor{})
}

// GcpExecutor executes the corresponding recipe step.
type GcpExecutor struct{}

// ExecuteStream streams the step execution.
func (e *GcpExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	stepIdx, kind, step, targets, ch, retryCfg, attemptMax := req.Index, req.Kind, req.Step, req.Targets, resCh, req.RetryCfg, req.AttemptMax
	gs, ok := step.(*cuetry.GcpStep)
	if !ok || gs.Gcp == nil {
		return fmt.Errorf("internal: gcp step missing gcp field")
	}

	b := step.Base()
	runAs := cuetry.EffectiveRunAs(b, opts.Recipe.Defaults)
	kvTunnel := cuetry.KVTunnelEnabled(step, opts.Recipe.Defaults)

	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		args := make([]string, 0, 3+2*len(gs.Gcp.Params))
		args = append(args, "gcloud", gs.Gcp.Service, gs.Gcp.Operation)

		for k, v := range gs.Gcp.Params {
			// e.g., bucket: "test" -> --bucket test
			strVal := fmt.Sprintf("%v", v)
			args = append(args, fmt.Sprintf("--%s", k), ShellSingleQuoted(strVal))
		}

		script := strings.Join(args, " ")
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
func (e *GcpExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, _ ExecutionOptions, out io.Writer) error {
	gs, _ := req.Step.(*cuetry.GcpStep)
	for _, target := range req.Targets {
		_, _ = fmt.Fprintf(out, "step %d: kind=gcp service=%q operation=%q host=%q\n", req.Index, gs.Gcp.Service, gs.Gcp.Operation, target.Record.Name)
	}
	return nil
}
