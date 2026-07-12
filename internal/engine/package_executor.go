package engine

import (
	"context"
	"fmt"
	"io"

	"github.com/shareed2k/honey/internal/cuetry"
)

func init() {
	RegisterStepExecutor(cuetry.KindPackage, &PackageExecutor{})
}

// PackageExecutor executes the corresponding recipe step.
type PackageExecutor struct{}

// ExecuteStream streams the step execution.
func (e *PackageExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	stepIdx, kind, step, targets, ch, retryCfg, attemptMax := req.Index, req.Kind, req.Step, req.Targets, resCh, req.RetryCfg, req.AttemptMax
	ps, ok := step.(*cuetry.PackageStep)
	if !ok || ps.Package == nil {
		return fmt.Errorf("internal: package step missing package field")
	}

	b := step.Base()
	runAs := cuetry.EffectiveRunAs(b, opts.Recipe.Defaults)
	kvTunnel := cuetry.KVTunnelEnabled(step, opts.Recipe.Defaults)

	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		name := ShellSingleQuoted(ps.Package.Name)
		var script string

		switch ps.Package.State {
		case "present", "latest":
			// Attempt to install the package using apt, dnf, yum, zypper, pacman, or apk
			script = fmt.Sprintf(`
if command -v apt-get >/dev/null 2>&1; then
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -qq >/dev/null 2>&1
	apt-get install -y -qq %s >/dev/null 2>&1
elif command -v dnf >/dev/null 2>&1; then
	dnf install -y -q %s >/dev/null 2>&1
elif command -v yum >/dev/null 2>&1; then
	yum install -y -q %s >/dev/null 2>&1
elif command -v zypper >/dev/null 2>&1; then
	zypper install -y -n -q %s >/dev/null 2>&1
elif command -v pacman >/dev/null 2>&1; then
	pacman -S --noconfirm -q %s >/dev/null 2>&1
elif command -v apk >/dev/null 2>&1; then
	apk add -q %s >/dev/null 2>&1
else
	echo "Unsupported package manager" >&2
	exit 1
fi
`, name, name, name, name, name, name)
		case "absent":
			script = fmt.Sprintf(`
if command -v apt-get >/dev/null 2>&1; then
	export DEBIAN_FRONTEND=noninteractive
	apt-get remove -y -qq %s >/dev/null 2>&1
elif command -v dnf >/dev/null 2>&1; then
	dnf remove -y -q %s >/dev/null 2>&1
elif command -v yum >/dev/null 2>&1; then
	yum remove -y -q %s >/dev/null 2>&1
elif command -v zypper >/dev/null 2>&1; then
	zypper remove -y -n -q %s >/dev/null 2>&1
elif command -v pacman >/dev/null 2>&1; then
	pacman -R --noconfirm -q %s >/dev/null 2>&1
elif command -v apk >/dev/null 2>&1; then
	apk del -q %s >/dev/null 2>&1
else
	echo "Unsupported package manager" >&2
	exit 1
fi
`, name, name, name, name, name, name)
		}

		remoteCmd, err := cuetry.WrapRemoteShell(runAs, script)
		if err != nil {
			return "echo " + ShellSingleQuoted("wrap err: "+err.Error())
		}
		return remoteCmd
	}

	post := CueRecipeSSHPostHostResult(ctx, opts, stepIdx, kind, step, kvTunnel)
	return StreamCommandParallel(ctx, opts.SSHUser, targets, kvTunnel, cmdFunc, ch, BatchOptions{
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
func (e *PackageExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, _ ExecutionOptions, out io.Writer) error {
	ps, _ := req.Step.(*cuetry.PackageStep)
	for _, target := range req.Targets {
		_, _ = fmt.Fprintf(out, "step %d: kind=package name=%q state=%q host=%q\n", req.Index, ps.Package.Name, ps.Package.State, target.Record.Name)
	}
	return nil
}
