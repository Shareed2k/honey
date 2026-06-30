package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/approval"
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

// RunnerOptions configures a RecipeRunner. All fields are injected at
// construction; the runner creates none of its own dependencies.
type RunnerOptions struct {
	ConfigPath   string
	Config       *config.File
	ExecRegistry hostexec.Registry
	Metrics      metrics.Observer
	Pools        *postgres.PoolManager
	Cache        *ClientCache      // optional shared SSH client cache; nil = per-run cache
	RecordDir    string            // "" disables session recording
	Enforcer     *policy.Enforcer  // optional OPA admission gate; nil = allow all
	Approvals    *approval.Store   // optional pending-approval store; nil = require_approval hard-denies
	Biometric    BiometricVerifier // optional WebAuthn token verifier; nil = require_biometric hard-denies
}

// BiometricVerifier verifies a biometric step-up token for an actor. Implemented
// by *webauthn.Manager; an interface here keeps the engine free of that import.
type BiometricVerifier interface {
	VerifyToken(actor, token string) bool
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
	ActorID string
	// ApprovalID references a previously-created approval that, once approved,
	// lets a require_approval recipe proceed.
	ApprovalID string
	// BiometricToken is a WebAuthn step-up token that satisfies a require_biometric
	// verdict for the actor.
	BiometricToken string
	Env            map[string]string
	AISystemPrompt string
	RecordSession  bool
	RecordLabel    string
	// CmdTimeout bounds each per-host remote command; 0 = no timeout.
	CmdTimeout time.Duration
	// Recorder, when non-nil, is used as-is and NOT closed by the runner (the
	// caller owns its lifecycle — needed by the async webhook, which must know
	// the recording ID before deferred execution). When nil and RecordSession
	// is true, the runner opens and closes its own recorder.
	Recorder *SessionRecorder

	// PluginManager provides the shared plugin manager. The caller borrows it
	// and is responsible for calling release() when the run is complete.
	PluginManager *plugins.Manager
}

// DryRun validates prompts and produces the recipe plan via the executor-based
// dry-run (each step's ExecuteDryRun), matching what a live run would attempt.
// When RecordSession is set (and no recorder is injected), it records the plan
// into a fresh recording.
func (r *RecipeRunner) DryRun(ctx context.Context, req RunRequest) (string, error) {
	validatedEnv, err := cuetry.ValidateAndApplyPromptDefaults(req.Recipe.PromptDefs(), req.Env)
	if err != nil {
		return "", err
	}
	req.Env = validatedEnv

	params, err := r.buildRunParams(req, req.PluginManager)
	if err != nil {
		return "", err
	}
	params.Execute = false // dry-run: render the plan, do not run steps

	var buf bytes.Buffer
	if runErr := RunCueRecipeSteps(ctx, &buf, params, nil); runErr != nil {
		return "", runErr
	}
	plan := buf.String()

	if req.Recorder != nil {
		r.recordRecipeMeta(req.Recorder, req, plan)
		if strings.TrimSpace(plan) == "" {
			req.Recorder.RecordData("plan", []byte("(empty plan)"))
		} else {
			req.Recorder.RecordData("plan", []byte(plan))
		}
	}

	return plan, nil
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
		CmdTimeout:     req.CmdTimeout,
	}, nil
}

// admitRecipe asks the OPA enforcer whether this actor may run this recipe, as
// a pre-execution admission gate. A nil enforcer (OPA disabled) always admits.
// When the policy returns require_approval, the run is held: if an approved
// approval id was supplied (and the policy then allows), it proceeds; otherwise
// a pending run is created and ErrPendingApproval is returned.
func (r *RecipeRunner) admitRecipe(ctx context.Context, req RunRequest) error {
	if r.opts.Enforcer == nil {
		return nil
	}
	actor := actorOrAPI(req.ActorID)
	d, err := r.evalAdmission(ctx, actor, req, nil)
	if err != nil {
		return fmt.Errorf("policy evaluation: %w", err)
	}

	switch d.Decision {
	case "require_approval":
		return r.handleApproval(ctx, actor, req, d)
	case "require_biometric":
		return r.handleBiometric(ctx, actor, req, d)
	}
	if !d.Allow {
		return fmt.Errorf("recipe admission: %s", reasonOr(d.DenyReason, "denied by policy"))
	}
	return nil
}

// evalAdmission runs the recipe_execute policy with optional execution extras
// (e.g. approved/approver, biometricVerified) for a re-evaluation after step-up.
func (r *RecipeRunner) evalAdmission(ctx context.Context, actor string, req RunRequest, exec map[string]any) (policy.Decision, error) {
	if exec == nil {
		exec = map[string]any{}
	}
	return r.opts.Enforcer.Evaluate(ctx, map[string]any{
		"action":    "recipe_execute",
		"actor":     actor,
		"recipe":    req.RecipeSourcePath,
		"hosts":     hostNames(req.Records),
		"execution": exec,
	})
}

// handleBiometric resolves a require_biometric verdict: a valid biometric token
// for the actor re-evaluates with biometricVerified=true; otherwise it denies.
func (r *RecipeRunner) handleBiometric(ctx context.Context, actor string, req RunRequest, d policy.Decision) error {
	if r.opts.Biometric != nil && req.BiometricToken != "" && r.opts.Biometric.VerifyToken(actor, req.BiometricToken) {
		again, err := r.evalAdmission(ctx, actor, req, map[string]any{"biometricVerified": true, "approver": actor})
		if err != nil {
			return fmt.Errorf("policy evaluation: %w", err)
		}
		if again.Allow {
			return nil
		}
	}
	return fmt.Errorf("recipe admission: %s", reasonOr(d.DenyReason, "biometric verification required"))
}

// handleApproval resolves a require_approval verdict: an approved id that the
// policy now allows proceeds; otherwise a pending run is created and
// ErrPendingApproval returned.
func (r *RecipeRunner) handleApproval(ctx context.Context, actor string, req RunRequest, d policy.Decision) error {
	if r.opts.Approvals != nil && req.ApprovalID != "" {
		if rec, ok := r.opts.Approvals.Get(req.ApprovalID); ok && rec.Status == approval.StatusApproved {
			again, err := r.evalAdmission(ctx, actor, req, map[string]any{"approved": true, "approver": rec.Approver})
			if err != nil {
				return fmt.Errorf("policy evaluation: %w", err)
			}
			if again.Allow {
				return nil
			}
		}
	}
	reason := reasonOr(d.DenyReason, "approval required")
	if r.opts.Approvals == nil {
		return fmt.Errorf("recipe admission: %s (no approval store configured)", reason)
	}
	rec := r.opts.Approvals.Create(actor, req.RecipeSourcePath, hostNames(req.Records), reason)
	return &ErrPendingApproval{ID: rec.ID, Reason: reason}
}

// ErrPendingApproval signals that a run is held pending human approval.
type ErrPendingApproval struct {
	ID     string
	Reason string
}

func (e *ErrPendingApproval) Error() string {
	return fmt.Sprintf("pending approval %s: %s", e.ID, e.Reason)
}

func reasonOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
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
// recorder-close lifecycle (when not injected), completing
// when the returned channel closes.
func (r *RecipeRunner) Execute(ctx context.Context, req RunRequest) (<-chan HostExecResult, error) {
	if err := r.admitRecipe(ctx, req); err != nil {
		return nil, err
	}

	validatedEnv, err := cuetry.ValidateAndApplyPromptDefaults(req.Recipe.PromptDefs(), req.Env)
	if err != nil {
		return nil, err
	}
	req.Env = validatedEnv

	params, err := r.buildRunParams(req, req.PluginManager)
	if err != nil {
		return nil, err
	}

	if req.Recorder != nil {
		r.recordRecipeMeta(req.Recorder, req, "")
	}

	ch := make(chan HostExecResult, recipeRunChannelCap)
	go func() {
		defer func() {
			close(ch) // last: consumers unblock only after cleanup
		}()

		inner := make(chan HostExecResult, recipeRunChannelCap)
		errCh := make(chan error, 1)
		go func() {
			defer close(inner)
			errCh <- StreamCueRecipeSteps(ctx, params, inner)
		}()

		for res := range inner {
			if req.Recorder != nil {
				req.Recorder.RecordHostExecResult(res)
			}
			ch <- res
		}

		if streamErr := <-errCh; streamErr != nil {
			if req.Recorder != nil {
				req.Recorder.RecordError(streamErr)
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
