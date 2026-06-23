package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/postgres"
)

// RecipeMetaHostLimit caps how many host records are copied into a recording's
// RecipeMeta. Standard across all run callers.
const RecipeMetaHostLimit = 200

// recipeRunChannelCap is the buffer size for streaming host-exec results.
const recipeRunChannelCap = 4096

// PluginProvider supplies a plugin manager for one recipe run plus a release
// func the runner calls when the run completes. Implementations differ in
// lifecycle: a shared, ref-counted cache (reuse, no close) for the synchronous
// request path; a fresh manager opened and closed per run for async paths.
type PluginProvider interface {
	Borrow() (*plugins.Manager, func())
}

// RunnerOptions configures a RecipeRunner. All fields are injected at
// construction; the runner creates none of its own dependencies.
type RunnerOptions struct {
	ConfigPath   string
	Config       *config.File
	ExecRegistry hostexec.Registry
	Metrics      metrics.Observer
	Pools        *postgres.PoolManager
	Cache        *ClientCache // optional shared SSH client cache; nil = per-run cache
	Plugins      PluginProvider
	RecordDir    string           // "" disables session recording
	Enforcer     *policy.Enforcer // optional OPA admission gate; nil = allow all
}

// RecipeRunner owns the full recipe-execution lifecycle: prompt validation,
// secret resolution, run-params assembly, session recording, and dry-run or
// streaming execution. Callers translate their own inputs into a RunRequest.
type RecipeRunner struct {
	opts RunnerOptions
}

// NewRecipeRunner builds a RecipeRunner from injected dependencies.
func NewRecipeRunner(opts RunnerOptions) *RecipeRunner {
	return &RecipeRunner{opts: opts}
}

// RunRequest is the high-level input: what recipe to run, against which hosts,
// with what env. The recipe is already parsed because parsing is caller-specific
// (webserver path/content resolution, webhook auth lookup, scheduler schedules).
type RunRequest struct {
	Recipe           cuetry.Recipe
	RecipeSourcePath string
	RecipeDir        string
	Records          []hosts.Record
	SSHUser          string
	// ActorID is the caller identity (JWT subject or trusted-proxy header),
	// used as OPA policy input. Empty resolves to "api" downstream.
	ActorID        string
	Env            map[string]string
	AISystemPrompt string
	RecordSession  bool
	RecordLabel    string
	// Recorder, when non-nil, is used as-is and NOT closed by the runner (the
	// caller owns its lifecycle — needed by the async webhook, which must know
	// the recording ID before deferred execution). When nil and RecordSession
	// is true, the runner opens and closes its own recorder.
	Recorder *SessionRecorder
}

// DryRun validates prompts and produces the recipe plan via the executor-based
// dry-run (each step's ExecuteDryRun), matching what a live run would attempt.
// When RecordSession is set (and no recorder is injected), it records the plan
// into a fresh recording. The borrowed plugin manager is always released before
// returning.
func (r *RecipeRunner) DryRun(ctx context.Context, req RunRequest) (string, error) {
	mgr, release := r.opts.Plugins.Borrow()
	defer release()

	validatedEnv, err := cuetry.ValidateAndApplyPromptDefaults(req.Recipe.PromptDefs(), req.Env)
	if err != nil {
		return "", err
	}
	req.Env = validatedEnv

	params, err := r.buildRunParams(req, mgr)
	if err != nil {
		return "", err
	}
	params.Execute = false // dry-run: render the plan, do not run steps

	var buf bytes.Buffer
	if runErr := RunCueRecipeSteps(ctx, &buf, params, nil); runErr != nil {
		return "", runErr
	}
	plan := buf.String()

	rec, ownRec, err := r.acquireRecorder(req)
	if err != nil {
		return "", err
	}
	if ownRec {
		defer func() { _ = rec.Close() }()
	}
	if rec != nil {
		r.recordRecipeMeta(rec, req, plan)
		if strings.TrimSpace(plan) == "" {
			rec.RecordData("plan", []byte("(empty plan)"))
		} else {
			rec.RecordData("plan", []byte(plan))
		}
	}

	return plan, nil
}

// acquireRecorder returns the recorder to use for a run. When req.Recorder is
// set, it is returned with ownRec=false (caller closes). Otherwise, when
// RecordSession is set and RecordDir is configured, a fresh recorder is opened
// with ownRec=true (runner closes). Returns (nil, false, nil) when no recording.
func (r *RecipeRunner) acquireRecorder(req RunRequest) (rec *SessionRecorder, ownRec bool, err error) {
	if req.Recorder != nil {
		return req.Recorder, false, nil
	}
	if !req.RecordSession || strings.TrimSpace(r.opts.RecordDir) == "" {
		return nil, false, nil
	}
	rec, err = NewBatchSessionRecorder(r.opts.RecordDir, req.RecordLabel, req.SSHUser, len(req.Records))
	if err != nil {
		return nil, false, err
	}
	return rec, true, nil
}

// recordRecipeMeta writes the recipe-meta event. plan may be "" (computed by
// the caller when already available, e.g. DryRun); when empty this renders it.
func (r *RecipeRunner) recordRecipeMeta(rec *SessionRecorder, req RunRequest, plan string) {
	if rec == nil {
		return
	}
	if plan == "" {
		plan, _, _ = cuetry.RenderDryRunPlan(req.Recipe)
	}
	hash, _ := req.Recipe.HashJSON()
	var graph *cuetry.RecipeGraphPlan
	if mode, err := cuetry.RecipeExecutionMode(req.Recipe); err == nil && mode == cuetry.ExecutionModeGraph {
		graph, _ = cuetry.BuildRecipeGraphPlan(req.Recipe)
	}
	rec.RecordRecipeMeta(RecipeMeta{
		RecipePath:        req.RecipeSourcePath,
		HostCount:         len(req.Records),
		RecipeContentHash: hash,
		StartedAt:         time.Now().UTC(),
		Hosts:             HostsForRecipeMeta(req.Records, RecipeMetaHostLimit),
		Plan:              plan,
		Graph:             graph,
	})
}

// buildRunParams assembles CueRecipeRunParams from a request and a borrowed
// plugin manager. Returns the secret-resolver construction error, if any.
func (r *RecipeRunner) buildRunParams(req RunRequest, mgr *plugins.Manager) (CueRecipeRunParams, error) {
	secRes, err := cuetry.NewSecretResolverWithPlugins(
		cuetry.SecretResolverOptionsFromHoney(r.opts.Config), mgr,
	)
	if err != nil {
		return CueRecipeRunParams{}, err
	}
	return CueRecipeRunParams{
		Recipe:         req.Recipe,
		RecipeDir:      req.RecipeDir,
		Records:        req.Records,
		SSHUser:        req.SSHUser,
		ActorID:        req.ActorID,
		CLIEnv:         req.Env,
		ConfigPath:     r.opts.ConfigPath,
		AISystemPrompt: req.AISystemPrompt,
		SecretResolver: secRes,
		PluginMgr:      mgr,
		Execute:        true,
		Obs:            r.opts.Metrics,
		Reg:            r.opts.ExecRegistry,
		Pools:          r.opts.Pools,
		Cache:          r.opts.Cache,
		Enforcer:       r.opts.Enforcer,
		Inventory:      invFromConfig(r.opts.Config),
	}, nil
}

// admitRecipe asks the OPA enforcer whether this actor may run this recipe, as
// a pre-execution admission gate. A nil enforcer (OPA disabled) always admits.
func (r *RecipeRunner) admitRecipe(ctx context.Context, req RunRequest) error {
	if r.opts.Enforcer == nil {
		return nil
	}
	actor := req.ActorID
	if actor == "" {
		actor = "api"
	}
	d, err := r.opts.Enforcer.Evaluate(ctx, map[string]any{
		"action": "recipe_execute",
		"actor":  actor,
		"recipe": req.RecipeSourcePath,
		"hosts":  hostNames(req.Records),
	})
	if err != nil {
		return fmt.Errorf("policy evaluation: %w", err)
	}
	if !d.Allow {
		reason := d.DenyReason
		if reason == "" {
			reason = "denied by policy"
		}
		return fmt.Errorf("recipe admission: %s", reason)
	}
	return nil
}

// invFromConfig returns the config inventory, or the zero value when no config.
func invFromConfig(f *config.File) config.Inventory {
	if f == nil {
		return config.Inventory{}
	}
	return f.Inventory
}

// hostNames extracts record names for policy input.
func hostNames(records []hosts.Record) []string {
	names := make([]string, len(records))
	for i, rec := range records {
		names[i] = rec.Name
	}
	return names
}

// Execute validates prompts, builds run params, and streams the recipe over its
// target hosts. Pre-flight errors (prompt validation, secret resolver, recorder
// creation) are returned synchronously. Once execution starts, run errors arrive
// on the channel as a synthetic failed HostExecResult. The runner owns the
// plugin-release and (when not injected) recorder-close lifecycle, completing
// when the returned channel closes.
func (r *RecipeRunner) Execute(ctx context.Context, req RunRequest) (<-chan HostExecResult, error) {
	if err := r.admitRecipe(ctx, req); err != nil {
		return nil, err
	}

	mgr, release := r.opts.Plugins.Borrow()

	validatedEnv, err := cuetry.ValidateAndApplyPromptDefaults(req.Recipe.PromptDefs(), req.Env)
	if err != nil {
		release()
		return nil, err
	}
	req.Env = validatedEnv

	params, err := r.buildRunParams(req, mgr)
	if err != nil {
		release()
		return nil, err
	}

	rec, ownRec, err := r.acquireRecorder(req)
	if err != nil {
		release()
		return nil, err
	}
	if ownRec {
		r.recordRecipeMeta(rec, req, "")
	}

	ch := make(chan HostExecResult, recipeRunChannelCap)
	go func() {
		defer func() {
			release()
			if ownRec && rec != nil {
				_ = rec.Close()
			}
			close(ch) // last: consumers unblock only after cleanup
		}()

		inner := make(chan HostExecResult, recipeRunChannelCap)
		errCh := make(chan error, 1)
		go func() {
			defer close(inner)
			errCh <- StreamCueRecipeSteps(ctx, params, inner)
		}()

		for res := range inner {
			if rec != nil {
				rec.RecordHostExecResult(res)
			}
			ch <- res
		}

		if streamErr := <-errCh; streamErr != nil {
			if rec != nil {
				rec.RecordError(streamErr)
			}
			ch <- HostExecResult{
				Name:     "recipe-run",
				Provider: "engine",
				Success:  false,
				ErrMsg:   streamErr.Error(),
			}
		}
	}()

	return ch, nil
}

// ExecuteAndWait runs the recipe to completion and discards the streamed host
// results — for callers that only need the run's side effects (session
// recording) and not the per-host stream. Pre-flight errors are returned as-is;
// a run failure surfaces as a non-nil error.
func (r *RecipeRunner) ExecuteAndWait(ctx context.Context, req RunRequest) error {
	ch, err := r.Execute(ctx, req)
	if err != nil {
		return err
	}
	var runErr error
	for res := range ch {
		if res.Provider == "engine" && res.Name == "recipe-run" && !res.Success {
			runErr = fmt.Errorf("recipe run failed: %s", res.ErrMsg)
		}
	}
	return runErr
}
