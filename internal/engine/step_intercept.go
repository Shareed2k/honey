package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/interceptwire"

	"github.com/shareed2k/mogate/pkg/local"
)

func init() {
	RegisterStepExecutor(cuetry.KindIntercept, &InterceptExecutor{})
}

// interceptLive is the subset of *intercept.Live the engine drives to run a
// step's command through an established interception session. A fake
// implements it in tests so the executor and coordinator are exercised
// without a real cluster; *intercept.Live (via sinkClosingLive) satisfies it
// in production.
type interceptLive interface {
	// Run runs one command through the already-deployed agent. Sequential
	// only: like the underlying *intercept.Live.Run, callers must not overlap
	// Run calls on the same interceptLive — the executor honors this by
	// construction, running at most one command per step.
	Run(ctx context.Context, runner intercept.LocalRunner, command []string) error
	// Close tears the session down, recording reason in the stop audit event.
	Close(reason string)
}

// sinkClosingLive wraps a *intercept.Live so its Close also closes the audit
// sink the session's Deps own. The CLI and intercept-pane callers close the
// sink in their own defer right after the one command they run; here the
// session outlives the executor call (it stays registered for session_step
// reuse across later steps), so the sink has to be closed alongside the
// session itself, when the coordinator tears it down at run end.
type sinkClosingLive struct {
	*intercept.Live
	sink audit.Sink
}

// Close tears down the underlying session, then closes the audit sink.
func (s *sinkClosingLive) Close(reason string) {
	s.Live.Close(reason)
	_ = s.sink.Close()
}

// establishInterceptSession builds the real Kubernetes/audit dependencies for
// opts.Cluster and establishes a live interception session against them. It
// is a package var so tests substitute a fake interceptLive instead of
// reaching for a real cluster; the default does the real thing.
var establishInterceptSession = func(ctx context.Context, cfg *config.File, opts intercept.Options) (interceptLive, error) {
	restCfg, err := interceptwire.RestConfigForCluster(cfg, opts.Cluster)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("intercept: build kubernetes client: %w", err)
	}
	deps, sink, err := interceptwire.BuildDeps(ctx, cfg, restCfg, clientset, opts.Namespace, opts.Pod, "")
	if err != nil {
		return nil, err
	}
	live, err := intercept.New(deps, opts).Establish(ctx)
	if err != nil {
		_ = sink.Close()
		return nil, err
	}
	return &sinkClosingLive{Live: live, sink: sink}, nil
}

// captureRunner is the intercept.LocalRunner an intercept step runs its
// command through: non-interactive (immediate stdin EOF, no pty) with
// stdout+stderr merged into one buffer, matching how command/script steps
// report Output.
type captureRunner struct{ buf *bytes.Buffer }

// Run executes command under the local injection session described by cfg,
// capturing its combined output into r.buf.
func (r captureRunner) Run(ctx context.Context, cfg local.Config, command []string) error {
	cfg.Pty = false
	cfg.Stdin = bytes.NewReader(nil) // EOF immediately (non-interactive)
	cfg.Stdout = r.buf
	cfg.Stderr = r.buf // merged into Output, matches docker/command steps
	return intercept.DefaultLocalRunner().Run(ctx, cfg, command)
}

// InterceptExecutor executes the corresponding recipe step: it is local-only
// (no RemoteExec fan-out) and runs its command/script exactly once per step,
// against either a freshly established interception session or one
// registered by an earlier step in the same run.
type InterceptExecutor struct{}

// ExecuteDryRun prints the step's plan without deploying anything.
func (e *InterceptExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error {
	if opts.Execute {
		return nil
	}
	is, _ := req.Step.(*cuetry.InterceptStep)
	if is == nil || is.Intercept == nil {
		return nil
	}
	ic := is.Intercept

	action := strings.TrimSpace(ic.Command)
	if action == "" {
		action = strings.TrimSpace(ic.Script)
	}

	if sessionStep := strings.TrimSpace(ic.SessionStep); sessionStep != "" {
		_, _ = fmt.Fprintf(out, "step %d: kind=intercept session_step=%q command=%q\n", req.Index, sessionStep, action)
	} else {
		target := "targeted"
		if ic.Targetless {
			target = "targetless"
		}
		_, _ = fmt.Fprintf(out, "step %d: kind=intercept %s cluster=%q namespace=%q modes=%v command=%q\n",
			req.Index, target, ic.Cluster, ic.Namespace, ic.Mode, action)
	}
	WriteCueStepNotifyDryLine(out, req.Step)
	return nil
}

// ExecuteStream streams the step execution: exactly one HostExecResult,
// regardless of req.Targets (interception runs locally, not per-host).
func (e *InterceptExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	i, step, out := req.Index, req.Step, resCh
	stepStart := time.Now()

	kv := KvReaderFromCoordinator(opts.RecipeKV)
	ok, whenErr := EvalAIStepWhen(ctx, opts.Recipe, step, opts.OutputStore, opts.SecretResolver, kv, opts.CLIEnv, opts.Execute)
	if whenErr != nil {
		return whenErr
	}

	if !ok {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | intercept", i+1),
			Provider: "local",
			Skipped:  true,
			Output:   "(skipped: when)",
		}
		AnnotateCueStepResult(&res, i, step, cuetry.KindIntercept)
		out <- res
		ObserveRecipeStep(opts.Obs, cuetry.KindIntercept, stepStart, []HostExecResult{res}, 1)
		return nil
	}

	res := runInterceptStep(ctx, i, step, opts)
	AnnotateCueStepResult(&res, i, step, cuetry.KindIntercept)
	out <- res
	ObserveRecipeStep(opts.Obs, cuetry.KindIntercept, stepStart, []HostExecResult{res}, 1)
	return nil
}

// runInterceptStep runs one intercept step's command/script against either a
// freshly established session (the establishing step) or a previously
// registered one (session_step reuse), returning the result whose ExitCode
// feeds failed_when/changed_when.
func runInterceptStep(ctx context.Context, i int, step cuetry.Step, opts ExecutionOptions) HostExecResult {
	res := HostExecResult{Name: fmt.Sprintf("Step %d | intercept", i+1), Provider: "local"}

	is, _ := step.(*cuetry.InterceptStep)
	if is == nil || is.Intercept == nil {
		res.ErrMsg = "internal: intercept step missing intercept block"
		res.ExitCode = 1
		return res
	}
	ic := is.Intercept

	var live interceptLive
	var err error
	if strings.TrimSpace(ic.SessionStep) == "" {
		live, err = establishInterceptStepSession(ctx, step, ic, opts)
	} else {
		var found bool
		live, found = opts.InterceptCoord.Lookup(ic.SessionStep)
		if !found {
			err = fmt.Errorf("intercept: session_step %q has no live session", ic.SessionStep)
		}
	}
	if err != nil {
		res.ErrMsg = err.Error()
		res.ExitCode = 1
		return res
	}

	buf := &bytes.Buffer{}
	runner := captureRunner{buf: buf}
	runErr := live.Run(ctx, runner, interceptCommand(ic))
	res.Output = buf.String()
	res.Success = runErr == nil
	if runErr != nil {
		res.ErrMsg = runErr.Error()
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = 1
		}
	}
	if ic.Output != "" && opts.OutputCapture != nil {
		opts.OutputCapture.Set(ic.Output, res.Output)
	}
	return res
}

// establishInterceptStepSession loads the honey config, enforces the
// intercept gate/cap, builds intercept.Options for a targetless agent, and
// establishes the session — registering it with opts.InterceptCoord under
// the step's id on success so a later session_step step (or the run-end
// coordinator Close) can find it. The established session is never closed
// here even though this step also runs its own command through it: closing
// it is the coordinator's job at run end.
func establishInterceptStepSession(ctx context.Context, step cuetry.Step, ic *cuetry.RecipeStepIntercept, opts ExecutionOptions) (interceptLive, error) {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("intercept: load config: %w", err)
	}
	if cfg.Intercept == nil || !cfg.Intercept.Enabled {
		return nil, errors.New("intercept is not enabled in the honey config")
	}
	if strings.TrimSpace(cfg.Intercept.AgentImage) == "" {
		return nil, errors.New("intercept: no agent image configured (set intercept.agent_image)")
	}
	if opts.InterceptCoord.Count() >= cfg.Intercept.MaxSessionsValue() {
		return nil, fmt.Errorf("intercept: max_sessions (%d) reached", cfg.Intercept.MaxSessionsValue())
	}

	pod, err := intercept.NewAgentPodName()
	if err != nil {
		return nil, fmt.Errorf("intercept: generate agent pod name: %w", err)
	}
	modes, err := intercept.ParseModes(ic.Mode)
	if err != nil {
		return nil, err
	}

	iopts := intercept.Options{
		Targetless: true,
		Pod:        pod,
		Namespace:  ic.Namespace,
		Cluster:    ic.Cluster,
		AgentImage: cfg.Intercept.AgentImage,
		Modes:      modes,
		UDP:        ic.UDP,
		EnvInclude: ic.EnvInclude,
		EnvExclude: ic.EnvExclude,
		Actor:      opts.ActorID,
	}

	live, err := establishInterceptSession(ctx, cfg, iopts)
	if err != nil {
		return nil, err
	}
	opts.InterceptCoord.Register(step.Base().ID, live)
	return live, nil
}

// interceptCommand builds the shell-wrapped command for a step: Validate
// already enforces that exactly one of Command/Script is set.
func interceptCommand(ic *cuetry.RecipeStepIntercept) []string {
	if cmd := strings.TrimSpace(ic.Command); cmd != "" {
		return []string{"/bin/sh", "-c", cmd}
	}
	return []string{"/bin/sh", "-c", ic.Script}
}
