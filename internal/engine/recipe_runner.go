package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/approval"
	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugincache"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/searchrun"
	"go.uber.org/zap"
)

// RecipeMetaHostLimit caps how many host records are copied into a recording's
// RecipeMeta. Standard across all run callers.
const RecipeMetaHostLimit = 200

// recipeRunChannelCap is the buffer size for streaming host-exec results.
const recipeRunChannelCap = 4096

// RunnerOptions configures a RecipeRunner. All fields are injected at
// construction; the runner creates none of its own dependencies.
type RunnerOptions struct {
	ConfigPath     string
	Config         *config.File
	ExecRegistry   hostexec.Registry
	SearchRegistry *searchrun.Registry // required for host resolution
	Metrics        metrics.Observer
	Pools          *postgres.PoolManager
	Cache          *ClientCache       // optional shared SSH client cache; nil = per-run cache
	PluginCache    *plugincache.Cache // optional shared plugin manager; nil = fresh manager per run
	RecordDir      string             // "" disables session recording
	Enforcer       *policy.Enforcer   // optional OPA admission gate; nil = allow all
	Approvals      *approval.Store    // optional pending-approval store; nil = require_approval hard-denies
	Biometric      BiometricVerifier  // optional WebAuthn token verifier; nil = require_biometric hard-denies
	AuditSink      audit.Sink         // optional; nil = no recipe_run admission audit
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

// PluginLifecycle defines how the plugin manager is managed during a run.
// PluginLifecycle defines how the plugin manager is handled for a run.
type PluginLifecycle string

const (
	// LifecycleShared uses a shared plugin cache.
	LifecycleShared PluginLifecycle = "shared"
	// LifecycleFresh creates a fresh plugin manager for the run.
	LifecycleFresh PluginLifecycle = "fresh"
)

// RunRequest is the high-level input: what recipe to run, against which hosts,
// with what env. The recipe is already parsed because parsing is caller-specific
// (webserver path/content resolution, webhook auth lookup, scheduler schedules).
type RunRequest struct {
	Recipe           cuetry.Recipe
	RecipeSourcePath string
	RecipeDir        string
	Target           *hostapi.SearchHostsInput
	Records          []hosts.Record // bypasses Target resolution if populated
	SSHUser          string
	// Source names the ingress that initiated this run ("web", "webhook",
	// "scheduler"); recorded on the recipe_run admission audit event.
	Source string
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

	PluginPolicy PluginLifecycle
}

// ErrTargetResolution marks a failure to resolve a RunRequest.Target into host
// records (search error or empty result), so callers can distinguish a bad
// target (a 400-class caller error) from an execution failure. Test with
// errors.Is.
var ErrTargetResolution = errors.New("target resolution failed")

// resolveTargets resolves hosts via SearchHosts if req.Records is empty and req.Target is provided.
func (r *RecipeRunner) resolveTargets(ctx context.Context, req *RunRequest) error {
	if len(req.Records) > 0 || req.Target == nil {
		return nil
	}
	if r.opts.SearchRegistry == nil {
		return fmt.Errorf("SearchRegistry is required for target resolution")
	}
	out, err := hostapi.SearchHosts(ctx, req.Target, r.opts.ExecRegistry, r.opts.SearchRegistry)
	if err != nil {
		return fmt.Errorf("%w: search hosts: %w", ErrTargetResolution, err)
	}
	if len(out.Records) == 0 {
		return fmt.Errorf("%w: no target hosts found", ErrTargetResolution)
	}
	req.Records = out.Records
	return nil
}

// borrowPluginManager borrows or opens a plugin manager for one Recipe run.
// The returned release function must be called after the run finishes.
func (r *RecipeRunner) borrowPluginManager(ctx context.Context, req RunRequest) (*plugins.Manager, func(), error) {
	if req.PluginPolicy == LifecycleShared && r.opts.PluginCache != nil {
		mgr, release := r.opts.PluginCache.Borrow()
		if mgr == nil {
			return nil, nil, fmt.Errorf("failed to borrow shared plugin manager")
		}
		return mgr, release, nil
	}

	mgr, err := plugins.Open(ctx, r.opts.Config)
	if err != nil {
		return nil, nil, fmt.Errorf("plugin open: %w", err)
	}
	return mgr, func() { _ = mgr.Close() }, nil
}

// withPluginManager borrows or opens a plugin manager, yields it to fn, and ensures
// it is released or closed afterwards.
func (r *RecipeRunner) withPluginManager(ctx context.Context, req RunRequest, fn func(*plugins.Manager) error) error {
	mgr, release, err := r.borrowPluginManager(ctx, req)
	if err != nil {
		return err
	}
	defer release()

	return fn(mgr)
}

// openRunRecorder returns the caller-provided recorder, or opens one owned by
// the runner when session recording is requested. owned reports whether the
// caller must close it.
func (r *RecipeRunner) openRunRecorder(req RunRequest) (*SessionRecorder, bool, error) {
	if req.Recorder != nil {
		return req.Recorder, false, nil
	}
	if !req.RecordSession || strings.TrimSpace(r.opts.RecordDir) == "" {
		return nil, false, nil
	}
	label := strings.TrimSpace(req.RecordLabel)
	if label == "" {
		label = "recipe-run"
	}
	rec, err := NewBatchSessionRecorder(r.opts.RecordDir, label, req.SSHUser, len(req.Records))
	if err != nil {
		return nil, false, fmt.Errorf("session recorder: %w", err)
	}
	return rec, true, nil
}

// DryRun validates prompts and produces the recipe plan via the executor-based
// dry-run (each step's ExecuteDryRun), matching what a live run would attempt.
// When RecordSession is set (and no recorder is injected), it records the plan
// into a fresh recording.
func (r *RecipeRunner) DryRun(ctx context.Context, req RunRequest) (string, error) {
	if err := r.resolveTargets(ctx, &req); err != nil {
		return "", err
	}

	validatedEnv, err := cuetry.ValidateAndApplyPromptDefaults(req.Recipe.PromptDefs(), req.Env)
	if err != nil {
		return "", err
	}
	req.Env = validatedEnv

	_, cleanupWS, err := SetupRecipeWorkspace(req.Env)
	if err != nil {
		return "", err
	}
	defer cleanupWS()

	var plan string
	err = r.withPluginManager(ctx, req, func(mgr *plugins.Manager) error {
		params, err := r.buildRunParams(req, mgr)
		if err != nil {
			return err
		}
		params.Execute = false // dry-run: render the plan, do not run steps

		var buf bytes.Buffer
		if runErr := RunCueRecipeSteps(ctx, &buf, params, nil); runErr != nil {
			return runErr
		}
		plan = buf.String()
		return nil
	})
	if err != nil {
		return "", err
	}

	rec, closeRec, err := r.openRunRecorder(req)
	if err != nil {
		return "", err
	}
	if closeRec {
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
		// No policy gate: the run is admitted. Audit it as allowed so every
		// executed run still yields a recipe_run event (matching the prior
		// handler behavior, which logged "allow" regardless of enforcer).
		r.auditRun(ctx, req, "allow", req.ApprovalID)
		return nil
	}
	actor := actorOrAPI(req.ActorID)
	d, err := r.evalAdmission(ctx, actor, req, nil)
	if err != nil {
		return fmt.Errorf("policy evaluation: %w", err)
	}

	switch d.Decision {
	case "require_approval":
		aerr := r.handleApproval(ctx, actor, req, d)
		var pending *ErrPendingApproval
		switch {
		case aerr == nil:
			r.auditRun(ctx, req, "allow", req.ApprovalID)
		case errors.As(aerr, &pending):
			r.auditRun(ctx, req, "require_approval", pending.ID)
		default:
			r.auditRun(ctx, req, "deny", "")
		}
		return aerr
	case "require_biometric":
		berr := r.handleBiometric(ctx, actor, req, d)
		if berr == nil {
			r.auditRun(ctx, req, "allow", req.ApprovalID)
		} else {
			r.auditRun(ctx, req, "require_biometric", "")
		}
		return berr
	}
	if !d.Allow {
		r.auditRun(ctx, req, "deny", "")
		return fmt.Errorf("recipe admission: %s", reasonOr(d.DenyReason, "denied by policy"))
	}
	r.auditRun(ctx, req, "allow", req.ApprovalID)
	return nil
}

// auditRun emits one recipe_run admission event to the configured AuditSink (nil
// sink = no-op). The verdict is authoritative here — admitRecipe is the single
// place the allow / require_approval / require_biometric / deny decision is made
// — so every run ingress (web, webhook, scheduler) audits consistently, closing
// the prior gap where the per-handler blocks never audited require_biometric.
func (r *RecipeRunner) auditRun(ctx context.Context, req RunRequest, decision, approvalID string) {
	if r.opts.AuditSink == nil {
		return
	}
	_ = r.opts.AuditSink.Log(ctx, audit.Event{
		Source:     req.Source,
		Actor:      req.ActorID,
		Action:     "recipe_run",
		Target:     req.Recipe.Name,
		Decision:   decision,
		ApprovalID: approvalID,
	})
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
	if err := r.resolveTargets(ctx, &req); err != nil {
		return nil, err
	}

	if err := r.admitRecipe(ctx, req); err != nil {
		return nil, err
	}

	validatedEnv, err := cuetry.ValidateAndApplyPromptDefaults(req.Recipe.PromptDefs(), req.Env)
	if err != nil {
		return nil, err
	}
	req.Env = validatedEnv

	_, cleanupWS, err := SetupRecipeWorkspace(req.Env)
	if err != nil {
		return nil, err
	}

	mgr, release, err := r.borrowPluginManager(ctx, req)
	if err != nil {
		cleanupWS()
		return nil, err
	}

	params, err := r.buildRunParams(req, mgr)
	if err != nil {
		cleanupWS()
		release()
		return nil, err
	}

	rec, closeRec, err := r.openRunRecorder(req)
	if err != nil {
		cleanupWS()
		release()
		return nil, err
	}
	if rec != nil {
		r.recordRecipeMeta(rec, req, "")
	}

	ch := make(chan HostExecResult, recipeRunChannelCap)
	go func() {
		defer cleanupWS()
		defer release()
		defer func() {
			if closeRec {
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
	var results []HostExecResult
	for res := range ch {
		results = append(results, res)
		if res.Provider == "engine" && res.Name == "recipe-run" && !res.Success {
			runErr = fmt.Errorf("recipe run failed: %s", res.ErrMsg)
		}
	}

	if r.opts.Config != nil && r.opts.Config.SMTP != nil {
		smtp := r.opts.Config.SMTP
		zap.L().Info("ExecuteAndWait initializing RunReporter", zap.String("host", smtp.Host), zap.Int("port", smtp.Port))
		reporter := NewRunReporter(smtp.Host, smtp.Port, smtp.Username, smtp.Password)
		reporter.Report(ctx, req.Recipe, results, runErr)
	}

	return runErr
}
