package engine

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/inventory"
	"github.com/shareed2k/honey/internal/plugins"
	"go.uber.org/zap"
)

// RunParams holds inputs for executing a recipe across hosts.
type RunParams struct {
	Recipe         cuetry.Recipe
	RecipeDir      string
	Records        []hosts.Record
	SSHUser        string
	Execute        bool
	CliEnv         map[string]string
	ConfigPath     string
	SecretResolver cuetry.SecretResolver
	PluginMgr      *plugins.Manager
}

// RunRecipe executes a recipe and emits lifecycle events.
func RunRecipe(_ context.Context, _ RunParams, events chan<- Event) error {
	defer close(events)
	// placeholder for migrating cue_recipe_run.go logic
	return nil
}

// RecipeHostMaxConc ...
func RecipeHostMaxConc(step cuetry.Step, defaults *cuetry.RecipeDefaults) int {
	re := RemoteOpts(step)
	if re != nil && re.Serial > 0 {
		return re.Serial
	}
	return re.EffectiveMaxParallel(defaults)
}

// CueEnvRunOpts ...
func CueEnvRunOpts(recipe *cuetry.Recipe, store *cuetry.StepOutputStore, capture *cuetry.RecipeOutputCapture, kv cuetry.KVReader, dryRun bool) *cuetry.EffectiveEnvForRunOpts {
	return &cuetry.EffectiveEnvForRunOpts{Recipe: recipe, OutputStore: store, OutputCapture: capture, KV: kv, DryRun: dryRun}
}

// StepEnv resolves the effective environment for one step on one target. All
// run-scoped inputs (secret resolver, recipe defaults, CLI env, prior step
// outputs, output capture, and live KV) come from the run; callers pass only
// what varies per step/target. Env cannot be pre-resolved before the run because
// OutputStore and KV are populated as earlier steps execute.
func (run *CueRun) StepEnv(ctx context.Context, step *cuetry.StepBase, target *hosts.Record, resolveSecrets, dryRun bool) (map[string]string, error) {
	return cuetry.EffectiveEnvForRunEx(
		ctx,
		resolveSecrets,
		run.Params.SecretResolver,
		step,
		run.Params.Recipe.Defaults,
		run.Params.CLIEnv,
		target,
		CueEnvRunOpts(&run.Params.Recipe, run.OutputStore, run.OutputCapture, KvReaderFromCoordinator(run.RecipeKV), dryRun),
	)
}

// WriteCueKVTunnelDryLine ...
func WriteCueKVTunnelDryLine(out io.Writer, recipe cuetry.Recipe, stepIdx int, step cuetry.Step, def *cuetry.RecipeDefaults) {
	if !cuetry.KVTunnelEnabled(step, def) {
		return
	}
	mode, _ := cuetry.RecipeExecutionMode(recipe)
	if mode == cuetry.ExecutionModeGraph {
		_, _ = fmt.Fprintf(out, "  step %d kv_tunnel: enabled — shared stepkv for the whole graph run (dependency waves order writes; same-wave steps share the session and may race — namespace with HONEY_STEP_ID and HONEY_HOST_NAME); remote env: HONEY_KV_URL, HONEY_KV_TOKEN\n", stepIdx)
		return
	}
	_, _ = fmt.Fprintf(out, "  step %d kv_tunnel: enabled — one operator stepkv for the whole cue-exec (SSH remote-forward, TrueNAS API shell bridge, or k8s exec multiplex per host; keys shared across steps and hosts; parallel hosts may race); remote env: HONEY_KV_URL, HONEY_KV_TOKEN (see docs)\n", stepIdx)
}

// WriteCueSSHPrivateKeyDryLine ...
func WriteCueSSHPrivateKeyDryLine(out io.Writer, stepIdx int, step cuetry.Step, def *cuetry.RecipeDefaults) {
	key := cuetry.EffectiveSSHPrivateKey(def, RemoteOpts(step))
	if key == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "  step %d ssh_private_key: %q (exclusive — only this key file is used for SSH; no ssh_config IdentityFile, %s, or default ~/.ssh keys)\n",
		stepIdx, key, "HONEY_SSH_IDENTITY_FILES")
}

// AnnotateCueStepResult ...
func AnnotateCueStepResult(res *HostExecResult, stepIdx int, step cuetry.Step, kind string) {
	if res == nil || step == nil {
		return
	}
	if stepIdx >= 0 {
		res.StepIndex = stepIdx + 1
	}
	res.StepID = strings.TrimSpace(step.Base().ID)
	res.StepKind = strings.TrimSpace(kind)
}

// CueAIDefaultMaxInputChars ...
const CueAIDefaultMaxInputChars = 200000

// GatherFacts ...
func (run *CueRun) GatherFacts(ctx context.Context) {
	if run.Params.Recipe.Defaults == nil || run.Params.Recipe.Defaults.GatherFacts == nil || !*run.Params.Recipe.Defaults.GatherFacts {
		return
	}
	zap.L().Debug("gathering host facts")
	encodedScript := base64.StdEncoding.EncodeToString([]byte(factsScript))
	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		return fmt.Sprintf("echo %s | base64 -d | sh", encodedScript)
	}
	var targetRecs []hosts.Record
	for _, r := range run.Params.Records {
		if strings.TrimSpace(r.PrimaryIP) != "" || (r.Provider == "k8s" && r.Meta["kind"] == "pod") {
			targetRecs = append(targetRecs, r)
		}
	}
	if len(targetRecs) == 0 {
		return
	}
	ch := make(chan HostExecResult, len(targetRecs))

	targetRecs = CueApplyRecipeSSHDialOptions(run.Params.Recipe, nil, targetRecs)

	var targets []TargetContext
	for _, r := range targetRecs {
		run.Facts[r.Name] = cuetry.DefaultFacts()
		targets = append(targets, TargetContext{Record: r}) // no env needed for facts gathering
	}

	err := StreamSSHParallel(ctx, run.Params.SSHUser, targets, false, cmdFunc, ch, BatchOptions{
		MaxConc:    8,
		Cache:      run.Cache,
		AttemptMax: nil,
		Obs:        nil,
	})
	close(ch)
	if err != nil {
		zap.L().Warn("failed to gather facts", zap.Error(err))
	}
	for res := range ch {
		if res.Success {
			var parsed map[string]any
			if err := json.Unmarshal([]byte(res.Output), &parsed); err != nil {
				zap.L().Warn("failed to parse host facts JSON", zap.String("host", res.Name), zap.Error(err))
				continue
			}
			run.Facts[res.Name] = parsed
		} else {
			zap.L().Warn("failed to gather facts on host", zap.String("host", res.Name), zap.String("err", res.ErrMsg))
		}
	}
}

//go:embed facts.sh
var factsScript string

// CueApplyRecipeSSHDialOptions ...
func CueApplyRecipeSSHDialOptions(recipe cuetry.Recipe, re *cuetry.RemoteExec, targets []hosts.Record) []hosts.Record {
	out := make([]hosts.Record, len(targets))
	for i, t := range targets {
		out[i] = cuetry.RecordForSSHDial(recipe.Defaults, re, t)
	}
	return out
}

// ExecuteStep dispatches execution to the appropriate step logic via StepExecutors.
func (run *CueRun) ExecuteStep(ctx context.Context, i int, kind string, step cuetry.Step, targets []hosts.Record, history [][]HostExecResult, ch chan<- HostExecResult, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) error {
	exec, err := GetStepExecutor(kind)
	if err != nil {
		return fmt.Errorf("execute step: %w", err)
	}

	var targetCtxs []TargetContext
	for _, t := range targets {
		env, err := run.StepEnv(ctx, step.Base(), &t, true, false)
		if err != nil {
			// If env resolution fails, we report an error for this host immediately.
			ch <- HostExecResult{
				Name:      t.Name,
				IP:        t.PrimaryIP,
				Provider:  t.Provider,
				StepIndex: i,
				StepID:    step.Base().ID,
				StepKind:  kind,
				Success:   false,
				ErrMsg:    fmt.Sprintf("resolve env: %v", err),
			}
			continue
		}
		targetCtxs = append(targetCtxs, TargetContext{Record: t, Env: env})
	}

	if len(targetCtxs) == 0 && len(targets) > 0 {
		// All targets failed env resolution
		return nil
	}

	req := ExecutionRequest{
		Targets:    targetCtxs,
		Index:      i,
		Step:       step,
		Kind:       kind,
		RetryCfg:   retryCfg,
		AttemptMax: attemptMax,
		History:    history,
	}

	opts := ExecutionOptions{
		Execute:           run.Params.Execute,
		Recipe:            run.Params.Recipe,
		RecipeDir:         run.Params.RecipeDir,
		SSHUser:           run.Params.SSHUser,
		CLIEnv:            run.Params.CLIEnv,
		SecretResolver:    run.Params.SecretResolver,
		PluginMgr:         run.Params.PluginMgr,
		Obs:               run.Params.Obs,
		Cache:             run.Cache,
		RecipeKV:          run.RecipeKV,
		ConfigPath:        run.Params.ConfigPath,
		Enforcer:          run.Params.Enforcer,
		Inventory:         run.Params.Inventory,
		CmdTimeout:        run.Params.CmdTimeout,
		Reg:               run.Params.Reg,
		Pools:             run.Params.Pools,
		Records:           run.Params.Records,
		OutputStore:       run.OutputStore,
		OutputCapture:     run.OutputCapture,
		Facts:             run.Facts,
		TriggeredHandlers: run.TriggeredHandlers,
	}

	proxyCh := make(chan HostExecResult)

	errCh := make(chan error, 1)
	go func() {
		errCh <- exec.ExecuteStream(ctx, req, opts, proxyCh)
		close(proxyCh)
	}()

	for res := range proxyCh {
		if err := EvaluateAssertions(&res, step.Base().Assert); err != nil {
			zap.L().Debug("step assertions failed", zap.Error(err), zap.String("step_id", step.Base().ID))
		}
		ch <- res
	}

	return <-errCh
}

// filterTargetsByPolicy asks the OPA enforcer which hosts this actor may run
// the step against, returning the allowed hosts and skip results for the denied
// ones (so they remain visible in the run output). A nil enforcer admits every
// host unchanged.
func filterTargetsByPolicy(ctx context.Context, run *CueRun, kind string, targets []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	if run.Params.Enforcer == nil || len(targets) == 0 {
		return targets, nil, nil
	}
	actor := actorOrAPI(run.Params.ActorID)

	kept := make([]hosts.Record, len(targets))
	skipped := make([]HostExecResult, len(targets))
	keepFlags := make([]bool, len(targets))

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(16) // Concurrency limit for OPA evaluation to prevent overwhelming the engine

	for i, t := range targets {
		i, t := i, t
		g.Go(func() error {
			d, err := run.Params.Enforcer.Evaluate(gCtx, map[string]any{
				"action":    "step_execute",
				"actor":     actor,
				"step_kind": kind,
				"host":      t.Name,
				"host_meta": t.Meta,
				"host_vars": hostVarsForPolicy(t, run.Params.Inventory),
			})
			if err != nil {
				return fmt.Errorf("policy evaluation for host %s: %w", t.Name, err)
			}
			if d.Allow {
				kept[i] = t
				keepFlags[i] = true
			} else {
				sk := WhenSkippedResult(t)
				reason := d.DenyReason
				if reason == "" {
					reason = "policy"
				}
				sk.Output = "(skipped: " + reason + ")"
				skipped[i] = sk
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}

	var finalKept []hosts.Record
	var finalSkipped []HostExecResult
	for i := range targets {
		if keepFlags[i] {
			finalKept = append(finalKept, kept[i])
		} else {
			finalSkipped = append(finalSkipped, skipped[i])
		}
	}
	return finalKept, finalSkipped, nil
}

// hostVarsForPolicy resolves a host's effective inventory vars (global + matching
// groups + host-specific) into a JSON-like map for OPA input.host_vars. It works
// on a one-element copy so the run's shared records are never mutated. A best-
// effort resolve: on error or empty inventory the record's existing vars are used.
func hostVarsForPolicy(rec hosts.Record, inv config.Inventory) map[string]any {
	cp := []hosts.Record{rec}
	_ = inventory.Apply(cp, inv)
	out := make(map[string]any, len(cp[0].Vars))
	for k, v := range cp[0].Vars {
		out[k] = v.Any()
	}
	return out
}

// CueGetLocalIsDirectory ...
func CueGetLocalIsDirectory(localField, absResolved string) (bool, error) {
	t := strings.TrimSpace(localField)
	if strings.HasSuffix(t, "/") || strings.HasSuffix(t, string(filepath.Separator)) {
		return true, nil
	}
	st, err := os.Stat(absResolved)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return st.IsDir(), nil
}

// CueSanitizeHostName ...
func CueSanitizeHostName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "host"
	}
	return s
}

// LoadTransferConfigFromConfigPath ...
func LoadTransferConfigFromConfigPath(configPath string) config.TransferConfigEffective {
	p := strings.TrimSpace(configPath)
	if p == "" {
		return (config.TransferConfig{}).WithDefaults()
	}
	f, err := config.Load(p)
	if err != nil || f == nil {
		return (config.TransferConfig{}).WithDefaults()
	}
	return f.Transfer.WithDefaults()
}

// TransferConfigFromSessionHoney ...
func TransferConfigFromSessionHoney(path string, f *config.File) config.TransferConfigEffective {
	if f != nil {
		return f.Transfer.WithDefaults()
	}
	return LoadTransferConfigFromConfigPath(path)
}

// StreamCueRecipeSteps ...
func StreamCueRecipeSteps(ctx context.Context, p CueRecipeRunParams, out chan<- HostExecResult) error {
	if len(p.Records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	tracer := otel.Tracer("honey")
	var span trace.Span
	ctx, span = tracer.Start(ctx, "recipe.run")
	span.SetAttributes(attribute.String("recipe.name", p.Recipe.Name))
	defer span.End()

	runStart := time.Now()
	var runErr error
	defer func() { ObserveRecipeRun(p.Obs, p.Recipe, true, runStart, runErr) }()

	ctx = cuetry.WithRecipeDir(ctx, p.RecipeDir)

	run := &CueRun{Params: p}
	if p.Cache != nil {
		run.Cache = p.Cache
	} else {
		run.Cache = NewClientCache()
		defer run.Cache.CloseAll()
	}
	run.Cache.SetRegistry(p.Reg)

	run.RecipeKV = NewRecipeKVCoordinator(0)
	defer run.RecipeKV.Close()
	run.TunnelCoord = NewRecipeTunnelCoordinator(nil)
	defer run.TunnelCoord.Close()
	if err := EnsureKVSessionForRecipe(p.Recipe, run.RecipeKV, p.Execute); err != nil {
		return err
	}

	run.Facts = make(map[string]map[string]any)
	run.GatherFacts(ctx)
	ctx = WithHostFacts(ctx, run.Facts)
	run.TriggeredHandlers = make(map[string]bool)

	mode, modeErr := cuetry.RecipeExecutionMode(p.Recipe)
	if modeErr != nil {
		runErr = modeErr
		return modeErr
	}
	if mode == cuetry.ExecutionModeGraph {
		runErr = StreamCueRecipeStepsGraph(ctx, run, out)
		return runErr
	}

	run.OutputStore = cuetry.NewStepOutputStore()
	run.OutputCapture = cuetry.NewRecipeOutputCapture()
	var history [][]HostExecResult
	for i, ws := range p.Recipe.Steps {
		step := ws.Step

		// Stop running further steps once the run is cancelled / timed out.
		if err := ctx.Err(); err != nil {
			runErr = err
			return err
		}

		rows, err := StreamCueRecipeStep(ctx, run, i, step, history, out)
		if len(rows) > 0 {
			history = append(history, rows)
		}
		if err != nil {
			if step.Base().IgnoreErrors {
				zap.L().Warn(
					"Step failed but ignore_errors is true. Continuing.",
					zap.Int("step_index", i+1),
					zap.Error(err),
				)
				err = nil
			} else {
				runErr = err
				return err
			}
		}
		if len(rows) > 0 && CueStepAllTargetsTransientTransportFailed(rows) {
			if step.Base().IgnoreErrors {
				zap.L().Warn(
					"Step failed with transport errors but ignore_errors is true. Continuing.",
					zap.Int("step_index", i+1),
				)
			} else {
				runErr = fmt.Errorf("step %d: all %d targets failed with transient transport errors; aborting recipe", i+1, len(rows))
				return runErr
			}
		}
		if step.Base().NotifyEnabled() && err == nil && len(rows) > 0 {
			body := FormatCueStepHostResultsForNotify(i+1, rows)
			CueStepNotifyRemote(ctx, p.Recipe, i+1, step.Kind(), step.Base().Notify, body)
		}
	}

	// Run triggered handlers
	if len(run.TriggeredHandlers) > 0 && len(p.Recipe.Handlers) > 0 {
		zap.L().Debug("executing triggered handlers", zap.Any("triggered", run.TriggeredHandlers))
		for _, handler := range p.Recipe.Handlers {
			hid := handler.Step.Base().ID
			if run.TriggeredHandlers[hid] {
				zap.L().Info("running handler", zap.String("id", hid))
				// Execute the handler step. Use step index -1 to indicate it is a handler.
				_, _ = StreamCueRecipeStep(ctx, run, -1, handler.Step, history, out)
			}
		}
	}

	return nil
}

// CueStepAllTargetsTransientTransportFailed ...
func CueStepAllTargetsTransientTransportFailed(results []HostExecResult) bool {
	var active []HostExecResult
	for _, r := range results {
		if !r.Skipped {
			active = append(active, r)
		}
	}
	if len(active) == 0 {
		return false
	}
	for _, r := range active {
		if r.Success {
			return false
		}
		if !IsSSHConnTransientError(errors.New(r.ErrMsg)) {
			return false
		}
	}
	return true
}

// StreamCueRecipeStep ...
func StreamCueRecipeStep(ctx context.Context, run *CueRun, i int, step cuetry.Step, history [][]HostExecResult, out chan<- HostExecResult) (stepResults []HostExecResult, err error) {
	stepStart := time.Now()
	var attemptMax atomic.Int32
	kind := step.Kind()

	tracer := otel.Tracer("honey")
	ctx, span := tracer.Start(ctx, fmt.Sprintf("step.%s", kind))
	span.SetAttributes(attribute.String("step.id", step.Base().ID))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	var targets []hosts.Record
	if CueRecipeLoopUsesItemHost(step) {
		targets = []hosts.Record{cuetry.MatchLocalAIHostRecord()}
	} else {
		targets, err = cuetry.ExpandStepHosts(step.Base().Host, run.Params.Records)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
	}
	targets = CueApplyRecipeSSHDialOptions(run.Params.Recipe, RemoteOpts(step), targets)

	// Apply pre-dispatch filters (policy gate → when clause) in sequence.
	// Skips accumulate and are emitted uniformly below.
	targets, allSkipped, err := newStepFilterPipelineForRun(run, kind, step).Apply(ctx, targets)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}

	if strings.TrimSpace(step.Base().Loop) != "" || step.Base().LoopFrom != nil {
		for _, sk := range allSkipped {
			res := sk
			res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
			out <- res
		}
		loopRes, loopErr := StreamCueLoopStep(ctx, run, i, step, targets, history, out)
		if reduceName := step.Base().Reduce; reduceName != "" && run.OutputCapture != nil {
			var collected []string
			for _, row := range loopRes {
				if row.Success && !row.Skipped {
					collected = append(collected, row.Output)
				}
			}
			if b, marshalErr := json.Marshal(collected); marshalErr == nil {
				run.OutputCapture.Set(reduceName, string(b))
			}
		}
		return loopRes, loopErr
	}

	whenSkipped := allSkipped

	ch := make(chan HostExecResult, len(targets))
	done := make(chan struct{})
	go func() {
		for _, sk := range whenSkipped {
			sk.OutputCapture = cuetry.StepOutputName(step)
			AnnotateCueStepResult(&sk, i, step, kind)
			stepResults = append(stepResults, sk)
			res := sk
			res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
			out <- res
		}
		for res := range ch {
			res.OutputCapture = cuetry.StepOutputName(step)
			AnnotateCueStepResult(&res, i, step, kind)
			stepResults = append(stepResults, res)
			res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
			out <- res
		}
		close(done)
	}()

	if len(targets) == 0 {
		close(ch)
		<-done
		RecordGraphStepStdout(run.Params.Recipe, step, kind, run.OutputStore, stepResults)
		ObserveRecipeStep(run.Params.Obs, kind, stepStart, stepResults, 1)
		return stepResults, nil
	}

	retryCfg := cuetry.EffectiveRetry(step.Base(), run.Params.Recipe.Defaults)

	err = run.ExecuteStep(ctx, i, kind, step, targets, history, ch, retryCfg, &attemptMax)

	close(ch)
	<-done
	RecordGraphStepStdout(run.Params.Recipe, step, kind, run.OutputStore, stepResults)
	if name := cuetry.StepOutputName(step); name != "" && run.OutputCapture != nil {
		for _, row := range stepResults {
			if row.Success && !row.Skipped {
				run.OutputCapture.Set(name, row.Output)
				break
			}
		}
	}
	if reduceName := step.Base().Reduce; reduceName != "" && run.OutputCapture != nil {
		var collected []string
		for _, row := range stepResults {
			if row.Success && !row.Skipped {
				collected = append(collected, row.Output)
			}
		}
		if b, err := json.Marshal(collected); err == nil {
			run.OutputCapture.Set(reduceName, string(b))
		}
	}
	maxAttempts := int(attemptMax.Load())
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	ObserveRecipeStep(run.Params.Obs, kind, stepStart, stepResults, maxAttempts)
	if err != nil {
		return stepResults, err
	}
	return stepResults, nil
}

// HostNameFromExecResult ...
func HostNameFromExecResult(name string) string {
	if i := strings.LastIndex(name, " | "); i >= 0 {
		return strings.TrimSpace(name[i+3:])
	}
	return strings.TrimSpace(name)
}

// CueRecipeLoopUsesItemHost ...
func CueRecipeLoopUsesItemHost(step cuetry.Step) bool {
	b := step.Base()
	if strings.TrimSpace(b.Loop) == "" && b.LoopFrom == nil {
		return false
	}
	return strings.TrimSpace(b.Host) == "${item}"
}

// CueRecipeLoopItems ...
func CueRecipeLoopItems(run *CueRun, step cuetry.Step, target hosts.Record) ([]string, error) {
	b := step.Base()
	if strings.TrimSpace(b.Loop) != "" {
		items, err := cuetry.RenderLoopTemplate(cuetry.RenderLoopTemplateOpts{
			Template: b.Loop,
			Store:    run.OutputStore,
			Capture:  run.OutputCapture,
		})
		if err != nil {
			return nil, fmt.Errorf("loop template failed: %w", err)
		}
		return items, nil
	}

	raw, ok := run.OutputStore.Get(b.LoopFrom.Step, target.Name)
	if !ok {
		raw, ok = run.OutputStore.FirstStdout(b.LoopFrom.Step)
	}
	if !ok {
		zap.L().Warn("loop_from: no raw output found for step", zap.String("step", b.LoopFrom.Step))
		return nil, nil
	}
	items, err := cuetry.EvalJQArray(raw, b.LoopFrom.Extract)
	if err != nil {
		return nil, fmt.Errorf("loop_from extraction failed: %w", err)
	}
	return items, nil
}

// StreamCueLoopStep ...
func StreamCueLoopStep(ctx context.Context, run *CueRun, i int, step cuetry.Step, targets []hosts.Record, history [][]HostExecResult, out chan<- HostExecResult) ([]HostExecResult, error) {
	var stepResults []HostExecResult
	for _, t := range targets {
		items, err := CueRecipeLoopItems(run, step, t)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			continue
		}
		for _, item := range items {
			loopStep := step.Clone()
			lb := loopStep.Base()
			if CueRecipeLoopUsesItemHost(step) {
				lb.Host = item
			}
			if lb.Env == nil {
				lb.Env = make(map[string]string)
			}
			lb.Env["item"] = item
			lb.Loop = ""
			lb.LoopFrom = nil

			runParams := run.Params
			if CueRecipeLoopUsesItemHost(step) {
				runParams.Records = run.Params.Records
			} else {
				runParams.Records = []hosts.Record{t}
			}
			loopRun := &CueRun{
				Params:            runParams,
				Cache:             run.Cache,
				RecipeKV:          run.RecipeKV,
				TunnelCoord:       run.TunnelCoord,
				OutputStore:       run.OutputStore,
				OutputCapture:     run.OutputCapture,
				Facts:             run.Facts,
				TriggeredHandlers: run.TriggeredHandlers,
			}

			subResults, err := StreamCueRecipeStep(ctx, loopRun, i, loopStep, history, out)
			if err == nil {
				for _, r := range subResults {
					if r.HookFailed {
						err = fmt.Errorf("loop step %d: hook failed on host %s: %s", i+1, r.Name, r.HookOutput)
						break
					}
				}
			}
			stepResults = append(stepResults, subResults...)
			if err != nil {
				if step.Base().IgnoreErrors {
					zap.L().Warn("loop step failed but ignore_errors is true. Continuing.", zap.Error(err))
				} else {
					return stepResults, err
				}
			}
		}
	}
	return stepResults, nil
}

// RecordGraphStepStdout ...
func RecordGraphStepStdout(recipe cuetry.Recipe, step cuetry.Step, kind string, store *cuetry.StepOutputStore, rows []HostExecResult) {
	if store == nil {
		return
	}
	id := strings.TrimSpace(step.Base().ID)
	if id == "" {
		return
	}
	RecordStepHostResults(store, id, rows)
	refs := cuetry.StepIDsReferencedByEnvFrom(recipe)
	if len(refs) == 0 {
		return
	}
	if _, ok := refs[id]; !ok {
		return
	}
	switch kind {
	case cuetry.KindCommand, cuetry.KindScript, cuetry.KindPlugin, cuetry.KindTunnel:
		for _, row := range rows {
			if row.Success && !row.Skipped {
				store.Record(id, HostNameFromExecResult(row.Name), row.Output)
			}
		}
	case cuetry.KindTemplate:
		for _, row := range rows {
			if row.Success && !row.Skipped {
				store.Record(id, cuetry.MatchLocalAIHost, row.Output)
			}
		}
	case cuetry.KindK8s:
		for _, row := range rows {
			if row.Success && !row.Skipped {
				store.Record(id, HostNameFromExecResult(row.Name), row.Output)
			}
		}
	}
}

// CueRecipeDisplayOutput ...
func CueRecipeDisplayOutput(res HostExecResult) string {
	if res.Success && res.KVCaptureKey != "" {
		return "[stored in kv: " + res.KVCaptureKey + "]"
	}
	if res.Success && res.OutputCapture != "" {
		return "[captured output: " + res.OutputCapture + "]"
	}
	return res.Output
}
