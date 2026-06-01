package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/postgres"
)

const cueAIDefaultMaxInputChars = 200000

// LoadAISystemPromptFromConfigPath returns defaults.ai_system_prompt from the honey YAML at path, if loadable.
func LoadAISystemPromptFromConfigPath(configPath string) string {
	p := strings.TrimSpace(configPath)
	if p == "" {
		return ""
	}
	f, err := config.Load(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(f.Defaults.AISystemPrompt)
}

// LoadTransferConfigFromConfigPath returns the effective transfer config from the
// honey YAML at path. If path is empty or the file fails to load, returns defaults.
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

// transferConfigFromSessionHoney returns effective transfer config from loaded file or path.
func transferConfigFromSessionHoney(path string, f *config.File) config.TransferConfigEffective {
	if f != nil {
		return f.Transfer.WithDefaults()
	}
	return LoadTransferConfigFromConfigPath(path)
}

func cueApplyRecipeSSHDialOptions(recipe cuetry.Recipe, step cuetry.RecipeStep, targets []hosts.Record) []hosts.Record {
	out := make([]hosts.Record, len(targets))
	for i, t := range targets {
		out[i] = cuetry.RecordForSSHDial(recipe.Defaults, step, t)
	}
	return out
}

// WriteCueSSHPrivateKeyDryLine prints one plan line when ssh_private_key is set for the step or defaults.
func WriteCueSSHPrivateKeyDryLine(out io.Writer, stepIdx int, step cuetry.RecipeStep, def *cuetry.RecipeDefaults) {
	key := cuetry.EffectiveSSHPrivateKey(def, step)
	if key == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "  step %d ssh_private_key: %q (exclusive — only this key file is used for SSH; no ssh_config IdentityFile, %s, or default ~/.ssh keys)\n",
		stepIdx, key, "HONEY_SSH_IDENTITY_FILES")
}

// StreamCueRecipeSteps executes a CUE recipe step-by-step, streaming results.
// configPath is the resolved honey YAML path (may be empty); agent_transfer steps with cloud_backend_ref require it.
// aiSystemPromptFromCfg is defaults.ai_system_prompt (already loaded), used only for the terminal ai step.
func StreamCueRecipeSteps(ctx context.Context, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, cliEnv map[string]string, configPath string, aiSystemPromptFromCfg string, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager, execute bool, obs metrics.Observer, out chan<- HostExecResult, reg hostexec.Registry, pools *postgres.PoolManager) error {
	if len(records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	runStart := time.Now()
	var runErr error
	defer func() { observeRecipeRun(obs, recipe, true, runStart, runErr) }()

	ctx = cuetry.WithRecipeDir(ctx, recipeDir)

	cache := NewClientCache()
	cache.SetRegistry(reg)
	defer cache.CloseAll()

	recipeKV := NewRecipeKVCoordinator(0)
	defer recipeKV.Close()
	tunnelCoord := NewRecipeTunnelCoordinator(nil)
	defer tunnelCoord.Close()
	if err := ensureKVSessionForRecipe(recipe, recipeKV, execute); err != nil {
		return err
	}

	mode, modeErr := cuetry.RecipeExecutionMode(recipe)
	if modeErr != nil {
		runErr = modeErr
		return modeErr
	}
	if mode == cuetry.ExecutionModeGraph {
		runErr = streamCueRecipeStepsGraph(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, aiSystemPromptFromCfg, secretResolver, pluginMgr, execute, obs, out, cache, recipeKV, tunnelCoord, reg, pools)
		return runErr
	}

	outputStore := cuetry.NewStepOutputStore()
	outputCapture := cuetry.NewRecipeOutputCapture()
	var history [][]HostExecResult
	for i, step := range recipe.Steps {
		kind, classifyErr := cuetry.ClassifyStep(step)
		if classifyErr != nil {
			return fmt.Errorf("step %d: %w", i, classifyErr)
		}
		if kind == cuetry.StepKindTemplate {
			stepStart := time.Now()
			rows, err := streamCueTemplateStep(ctx, recipe, recipeDir, i, step, records, outputStore, outputCapture, recipeKV, secretResolver, execute, out)
			observeRecipeStep(obs, kind, stepStart, rows, 1)
			if len(rows) > 0 {
				history = append(history, rows)
			}
			if err != nil {
				runErr = err
				return err
			}
			continue
		}
		if kind == cuetry.StepKindAI {
			stepStart := time.Now()
			kv := kvReaderFromCoordinator(recipeKV)
			ok, whenErr := evalAIStepWhen(ctx, recipe, step, outputStore, secretResolver, kv, cliEnv, execute)
			if whenErr != nil {
				runErr = whenErr
				return whenErr
			}
			if !ok {
				res := HostExecResult{
					Name:     fmt.Sprintf("Step %d | ai", i+1),
					Provider: "local",
					Skipped:  true,
					Output:   "(skipped: when)",
				}
				out <- res
				rows := []HostExecResult{res}
				observeRecipeStep(obs, kind, stepStart, rows, 1)
				history = append(history, rows)
				continue
			}
			res := runCueStepAIExecute(ctx, recipe, i, step, history, aiSystemPromptFromCfg)
			out <- res
			rows := []HostExecResult{res}
			observeRecipeStep(obs, kind, stepStart, rows, 1)
			history = append(history, rows)
			continue
		}
		rows, err := streamCueRecipeStep(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, i, step, out, cache, recipeKV, tunnelCoord, outputStore, outputCapture, secretResolver, pluginMgr, execute, obs, reg, pools)
		if len(rows) > 0 {
			history = append(history, rows)
		}
		if err != nil {
			runErr = err
			return err
		}
		if len(rows) > 0 && cueStepAllTargetsTransientTransportFailed(rows) {
			runErr = fmt.Errorf("step %d: all %d targets failed with transient transport errors; aborting recipe", i+1, len(rows))
			return runErr
		}
		if step.NotifyEnabled() && err == nil && len(rows) > 0 {
			body := FormatCueStepHostResultsForNotify(i+1, rows)
			CueStepNotifyRemote(ctx, recipe, i+1, kind, step.Notify, body)
		}
	}
	return nil
}

// cueStepAllTargetsTransientTransportFailed reports whether every host result for
// the step looks like a transient SSH/transport failure (so continuing the recipe
// would likely repeat the same outage).
func cueStepAllTargetsTransientTransportFailed(results []HostExecResult) bool {
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

func recipeHostMaxConc(step cuetry.RecipeStep, defaults *cuetry.RecipeDefaults) int {
	return cuetry.EffectiveMaxParallel(step, defaults)
}

func cueEnvRunOpts(recipe *cuetry.Recipe, store *cuetry.StepOutputStore, capture *cuetry.RecipeOutputCapture, kv cuetry.KVReader, dryRun bool) *cuetry.EffectiveEnvForRunOpts {
	return &cuetry.EffectiveEnvForRunOpts{Recipe: recipe, OutputStore: store, OutputCapture: capture, KV: kv, DryRun: dryRun}
}

func recordGraphStepStdout(recipe cuetry.Recipe, step cuetry.RecipeStep, kind cuetry.StepKind, store *cuetry.StepOutputStore, rows []HostExecResult) {
	if store == nil {
		return
	}
	id := strings.TrimSpace(step.ID)
	if id == "" {
		return
	}
	recordStepHostResults(store, id, rows)
	refs := cuetry.StepIDsReferencedByEnvFrom(recipe)
	if len(refs) == 0 {
		return
	}
	if _, ok := refs[id]; !ok {
		return
	}
	switch kind {
	case cuetry.StepKindCommand, cuetry.StepKindScript, cuetry.StepKindPlugin, cuetry.StepKindTunnel:
		for _, row := range rows {
			if row.Success && !row.Skipped {
				store.Record(id, hostNameFromExecResult(row.Name), row.Output)
			}
		}
	case cuetry.StepKindTemplate:
		for _, row := range rows {
			if row.Success && !row.Skipped {
				store.Record(id, cuetry.MatchLocalAIHost, row.Output)
			}
		}
	}
}

func hostNameFromExecResult(name string) string {
	if i := strings.LastIndex(name, " | "); i >= 0 {
		return strings.TrimSpace(name[i+3:])
	}
	return strings.TrimSpace(name)
}

func streamCueRecipeStep(ctx context.Context, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, cliEnv map[string]string, configPath string, i int, step cuetry.RecipeStep, out chan<- HostExecResult, cache *ClientCache, recipeKV *RecipeKVCoordinator, tunnelCoord *RecipeTunnelCoordinator, outputStore *cuetry.StepOutputStore, outputCapture *cuetry.RecipeOutputCapture, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager, execute bool, obs metrics.Observer, reg hostexec.Registry, pools *postgres.PoolManager) ([]HostExecResult, error) {
	stepStart := time.Now()
	var attemptMax atomic.Int32
	kind, classifyErr := cuetry.ClassifyStep(step)
	if classifyErr != nil {
		return nil, fmt.Errorf("step %d: %w", i, classifyErr)
	}
	if kind == cuetry.StepKindAgentTransfer {
		rows, err := streamCueStepAgentTransferWhen(ctx, recipe, records, sshUser, configPath, i, step, cache, outputStore, secretResolver, recipeKV, cliEnv, execute)
		for _, r := range rows {
			out <- r
		}
		recordGraphStepStdout(recipe, step, kind, outputStore, rows)
		observeRecipeStep(obs, kind, stepStart, rows, 1)
		return rows, err
	}

	targets, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}
	targets = cueApplyRecipeSSHDialOptions(recipe, step, targets)

	kv := kvReaderFromCoordinator(recipeKV)
	var whenSkipped []HostExecResult
	if strings.TrimSpace(step.When) != "" {
		targets, whenSkipped, err = filterTargetsByWhen(ctx, recipe, step, targets, outputStore, secretResolver, kv, cliEnv, execute)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
	}

	ch := make(chan HostExecResult, len(targets))
	done := make(chan struct{})
	var stepResults []HostExecResult
	go func() {
		for _, sk := range whenSkipped {
			stepResults = append(stepResults, sk)
			res := sk
			res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
			out <- res
		}
		for res := range ch {
			stepResults = append(stepResults, res)
			res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
			out <- res
		}
		close(done)
	}()

	if len(targets) == 0 {
		close(ch)
		<-done
		recordGraphStepStdout(recipe, step, kind, outputStore, stepResults)
		observeRecipeStep(obs, kind, stepStart, stepResults, 1)
		return stepResults, nil
	}

	execCache := cache

	retryCfg := cuetry.EffectiveRetry(step, recipe.Defaults)

	var stepErr error
	switch kind {
	case cuetry.StepKindCommand:
		stepErr = streamCueStepCommand(ctx, recipe, recipeDir, i, kind, step, cliEnv, sshUser, targets, ch, execCache, recipeKV, outputStore, outputCapture, secretResolver, pluginMgr, retryCfg, obs, &attemptMax, reg)

	case cuetry.StepKindPut:
		stepErr = streamCueStepPut(ctx, recipeDir, recipe, step, sshUser, targets, ch, cache, retryCfg, obs, &attemptMax)

	case cuetry.StepKindGet:
		stepErr = streamCueStepGet(ctx, recipeDir, recipe, step, sshUser, targets, ch, cache, retryCfg, obs, &attemptMax)

	case cuetry.StepKindScript:
		stepErr = streamCueStepScript(ctx, recipe, recipeDir, i, kind, step, cliEnv, sshUser, targets, ch, execCache, recipeKV, outputStore, outputCapture, secretResolver, pluginMgr, retryCfg, obs, &attemptMax, reg)

	case cuetry.StepKindPlugin:
		stepErr = streamCueStepPlugin(ctx, recipe, recipeDir, i, kind, step, cliEnv, sshUser, targets, ch, pluginMgr, secretResolver, execute, cache, recipeKV, tunnelCoord, outputStore, outputCapture, retryCfg, obs, &attemptMax, reg, pools)

	case cuetry.StepKindTunnel:
		stepErr = streamCueStepTunnel(ctx, recipe, i, step, sshUser, targets, ch, cache, tunnelCoord, execute, retryCfg, obs, &attemptMax)
	}

	close(ch)
	<-done
	recordGraphStepStdout(recipe, step, kind, outputStore, stepResults)
	maxAttempts := int(attemptMax.Load())
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	observeRecipeStep(obs, kind, stepStart, stepResults, maxAttempts)
	if stepErr != nil {
		return stepResults, stepErr
	}
	return stepResults, nil
}

func streamCueStepAgentTransferWhen(
	ctx context.Context,
	recipe cuetry.Recipe,
	records []hosts.Record,
	sshUser, configPath string,
	i int,
	step cuetry.RecipeStep,
	cache *ClientCache,
	outputStore *cuetry.StepResultStore,
	secretResolver cuetry.SecretResolver,
	recipeKV *RecipeKVCoordinator,
	cliEnv map[string]string,
	execute bool,
) ([]HostExecResult, error) {
	at := step.AgentTransfer
	if at == nil {
		return nil, fmt.Errorf("step %d: internal: missing agent_transfer", i)
	}
	srcHosts, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}
	dstHosts, err := cuetry.ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return nil, fmt.Errorf("step %d dest_host: %w", i, err)
	}
	if len(srcHosts) != 1 || len(dstHosts) != 1 {
		return streamCueStepAgentTransfer(ctx, records, sshUser, configPath, i, step, cache)
	}
	src := srcHosts[0]
	dst := dstHosts[0]
	kv := kvReaderFromCoordinator(recipeKV)
	ok, err := evalAgentTransferWhen(ctx, recipe, step, src, dst, outputStore, secretResolver, kv, cliEnv, execute)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}
	if !ok {
		res := whenSkippedResult(src)
		res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
		return []HostExecResult{res}, nil
	}
	return streamCueStepAgentTransfer(ctx, records, sshUser, configPath, i, step, cache)
}

// WriteCueKVTunnelDryLine prints one plan line when kv_tunnel is enabled for the step or defaults.
func WriteCueKVTunnelDryLine(out io.Writer, recipe cuetry.Recipe, stepIdx int, step cuetry.RecipeStep, def *cuetry.RecipeDefaults) {
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

func streamCueStepCommand(ctx context.Context, recipe cuetry.Recipe, recipeDir string, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, cliEnv map[string]string, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, execCache *ClientCache, recipeKV *RecipeKVCoordinator, outputStore *cuetry.StepOutputStore, outputCapture *cuetry.RecipeOutputCapture, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager, retryCfg cuetry.RecipeStepRetry, obs metrics.Observer, attemptMax *atomic.Int32, reg hostexec.Registry) error {
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	kvTunnel := cuetry.KVTunnelEnabled(step, recipe.Defaults)

	cmdFunc := func(r hosts.Record, kv map[string]string) string {
		env, err := cuetry.EffectiveEnvForRunEx(ctx, true, secretResolver, step, recipe.Defaults, cliEnv, &r, cueEnvRunOpts(&recipe, outputStore, outputCapture, kvReaderFromCoordinator(recipeKV), false))
		if err != nil {
			return fmt.Sprintf("echo 'env err: %s'", err.Error())
		}
		for k, v := range kv {
			env[k] = v
		}
		inner, err := cuetry.ShellExportPrefixForRemote(env, strings.TrimSpace(step.Command))
		if err != nil {
			return fmt.Sprintf("echo 'export err: %s'", err.Error())
		}
		remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
		if err != nil {
			return fmt.Sprintf("echo 'wrap err: %s'", err.Error())
		}
		return remoteCmd
	}

	recipeScoped := kvTunnel
	post := cueRecipeSSHPostHostResult(ctx, recipe, stepIdx, kind, step, recipeDir, sshUser, cliEnv, execCache, recipeKV, recipeScoped, secretResolver, pluginMgr)
	return StreamSSHParallel(ctx, sshUser, targets, kvTunnel, cmdFunc, recipeHostMaxConc(step, recipe.Defaults), ch, execCache, recipeKV, recipeScoped, post, retryCfg, obs, attemptMax, reg)
}

func streamCueStepPut(ctx context.Context, recipeDir string, recipe cuetry.Recipe, step cuetry.RecipeStep, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, cache *ClientCache, retryCfg cuetry.RecipeStepRetry, obs metrics.Observer, attemptMax *atomic.Int32) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Put.Local)
	if err != nil {
		return fmt.Errorf("put.local: %w", err)
	}
	remotePath := strings.TrimSpace(step.Put.Remote)
	if _, statErr := os.Stat(localAbs); statErr != nil {
		return fmt.Errorf("put: local file %q: %w", localAbs, statErr)
	}
	return StreamSFTPUploadParallel(ctx, sshUser, targets, localAbs, remotePath, recipeHostMaxConc(step, recipe.Defaults), ch, cache, retryCfg, obs, attemptMax)
}

func streamCueStepGet(ctx context.Context, recipeDir string, recipe cuetry.Recipe, step cuetry.RecipeStep, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, cache *ClientCache, retryCfg cuetry.RecipeStepRetry, obs metrics.Observer, attemptMax *atomic.Int32) error {
	remotePath := strings.TrimSpace(step.Get.Remote)
	localRoot, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Get.Local)
	if err != nil {
		return fmt.Errorf("get.local: %w", err)
	}
	if len(targets) > 1 {
		ok, err := cueGetLocalIsDirectory(step.Get.Local, localRoot)
		if err != nil {
			return fmt.Errorf("get: %w", err)
		} else if !ok {
			return fmt.Errorf("get: %d hosts require get.local to be a directory; got %q", len(targets), step.Get.Local)
		}
	}
	jobs := make([]SFTPDownloadJob, 0, len(targets))
	base := filepath.Base(remotePath)
	if base == "." || base == "/" {
		base = "download"
	}
	for _, target := range targets {
		dest := localRoot
		if len(targets) > 1 {
			dest = filepath.Join(localRoot, cueSanitizeHostName(target.Name)+"_"+base)
		}
		jobs = append(jobs, SFTPDownloadJob{
			Record:     target,
			LocalAbs:   dest,
			RemotePath: remotePath,
		})
	}
	if len(targets) > 1 {
		if err := os.MkdirAll(localRoot, 0o750); err != nil {
			return fmt.Errorf("get: mkdir %q: %w", localRoot, err)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(jobs[0].LocalAbs), 0o750); err != nil {
			return fmt.Errorf("get: mkdir parent: %w", err)
		}
	}
	return StreamSFTPDownloadParallel(ctx, sshUser, jobs, recipeHostMaxConc(step, recipe.Defaults), ch, cache, retryCfg, obs, attemptMax)
}

func streamCueStepScript(ctx context.Context, recipe cuetry.Recipe, recipeDir string, stepIdx int, kind cuetry.StepKind, step cuetry.RecipeStep, cliEnv map[string]string, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, execCache *ClientCache, recipeKV *RecipeKVCoordinator, outputStore *cuetry.StepOutputStore, outputCapture *cuetry.RecipeOutputCapture, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager, retryCfg cuetry.RecipeStepRetry, obs metrics.Observer, attemptMax *atomic.Int32, _ hostexec.Registry) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Script.Local)
	if err != nil {
		return fmt.Errorf("script.local: %w", err)
	}
	remotePath := strings.TrimSpace(step.Script.Remote)
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	kvTunnel := cuetry.KVTunnelEnabled(step, recipe.Defaults)

	if _, statErr := os.Stat(localAbs); statErr != nil {
		return fmt.Errorf("script: local file %q: %w", localAbs, statErr)
	}

	cmdFunc := func(r hosts.Record, kv map[string]string) string {
		env, err := cuetry.EffectiveEnvForRunEx(ctx, true, secretResolver, step, recipe.Defaults, cliEnv, &r, cueEnvRunOpts(&recipe, outputStore, outputCapture, kvReaderFromCoordinator(recipeKV), false))
		if err != nil {
			return fmt.Sprintf("echo 'env err: %s'", err.Error())
		}
		for k, v := range kv {
			env[k] = v
		}
		remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs, env)
		if err != nil {
			return fmt.Sprintf("echo 'wrap err: %s'", err.Error())
		}
		return remoteCmd
	}

	recipeScoped := kvTunnel
	post := cueRecipeSSHPostHostResult(ctx, recipe, stepIdx, kind, step, recipeDir, sshUser, cliEnv, execCache, recipeKV, recipeScoped, secretResolver, pluginMgr)
	return StreamScriptUploadRunParallel(ctx, sshUser, targets, localAbs, remotePath, kvTunnel, cmdFunc, recipeHostMaxConc(step, recipe.Defaults), ch, execCache, recipeKV, recipeScoped, post, retryCfg, obs, attemptMax)
}

// RunCueRecipeSteps executes a CUE recipe over a slice of target records without streaming.
// cliEnv is merged into each command/script step's remote env (overrides recipe env on duplicate keys); nil is treated as empty.
// configPath is the resolved honey YAML path (may be empty); agent_transfer with cloud_backend_ref requires it.
// rec, when non-nil, records a batch .hrec.jsonl (plan on dry-run, result rows on execute). Caller must Close(rec).
func RunCueRecipeSteps(ctx context.Context, out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string, configPath string, aiSystemPromptFromCfg string, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager, rec *SessionRecorder, obs metrics.Observer, reg hostexec.Registry, pools *postgres.PoolManager) error {
	if len(records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	runStart := time.Now()
	var runErr error
	if !execute {
		defer func() { observeRecipeRun(obs, recipe, false, runStart, runErr) }()
	}

	if !execute {
		outWrite := out
		var capture bytes.Buffer
		if rec != nil {
			outWrite = io.MultiWriter(out, &capture)
		}
		mode, modeErr := cuetry.RecipeExecutionMode(recipe)
		if modeErr != nil {
			runErr = modeErr
			return modeErr
		}
		if mode == cuetry.ExecutionModeGraph {
			text, err := cuetry.FormatGraphWavesText(recipe)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(outWrite, text)
		}
		for i, step := range recipe.Steps {
			if err := runCueRecipeStep(outWrite, recipe, recipeDir, records, sshUser, false, cliEnv, configPath, i, step, secretResolver, pluginMgr); err != nil {
				if rec != nil {
					rec.RecordError(err)
				}
				runErr = err
				return err
			}
		}
		_, _ = fmt.Fprintln(outWrite, "\nDry-run only. Append ! to the path in the TUI to execute, or use honey cue-exec --execute.")
		if rec != nil {
			plan := capture.String()
			if strings.TrimSpace(plan) == "" {
				rec.RecordData("plan", []byte("(empty plan)"))
			} else {
				rec.RecordData("plan", []byte(plan))
			}
		}
		return nil
	}

	// Second execution path: actual execution via streaming logic
	ch := make(chan HostExecResult)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		errCh <- StreamCueRecipeSteps(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, aiSystemPromptFromCfg, secretResolver, pluginMgr, true, obs, ch, reg, pools)
	}()

	for res := range ch {
		if rec != nil {
			rec.RecordHostExecResult(res)
		}
		status := "ok"
		if !res.Success {
			status = "FAILED"
		}
		// The Name already includes the "Step X | " prefix from streamCueRecipeStep
		_, _ = fmt.Fprintf(out, "[%s] %s @ %s — %s", res.Provider, res.Name, res.IP, status)
		if res.ErrMsg != "" {
			_, _ = fmt.Fprintf(out, " — %s", res.ErrMsg)
		}
		_, _ = fmt.Fprintln(out)
		if strings.TrimSpace(res.Output) != "" {
			_, _ = fmt.Fprintln(out, strings.TrimSpace(res.Output))
		}
		if strings.TrimSpace(res.HookPhase) != "" || strings.TrimSpace(res.HookOutput) != "" {
			if strings.TrimSpace(res.HookOutput) != "" {
				_, _ = fmt.Fprintf(out, "hook (%s):\n%s\n", strings.TrimSpace(res.HookPhase), strings.TrimSpace(res.HookOutput))
			} else {
				_, _ = fmt.Fprintf(out, "hook (%s): (no output)\n", strings.TrimSpace(res.HookPhase))
			}
		}
	}
	streamErr := <-errCh
	if streamErr != nil {
		if rec != nil {
			rec.RecordError(streamErr)
		}
		runErr = streamErr
		return streamErr
	}
	return nil
}

func runCueRecipeStep(out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string, configPath string, i int, step cuetry.RecipeStep, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager) error {
	zap.L().Debug("evaluating cue step", zap.Int("step_index", i), zap.String("host", step.Host))
	kind, err := cuetry.ClassifyStep(step)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	if kind == cuetry.StepKindAgentTransfer {
		return runCueStepAgentTransferDry(out, records, sshUser, configPath, i, step)
	}
	if kind == cuetry.StepKindAI {
		return runCueStepAIDry(out, recipe, execute, i, step)
	}
	if kind == cuetry.StepKindTemplate {
		return runCueStepTemplateDry(out, execute, i, step)
	}
	targets, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	targets = cueApplyRecipeSSHDialOptions(recipe, step, targets)
	if !execute && strings.TrimSpace(step.When) != "" {
		if err := writeWhenDryLines(out, i, step, recipe, targets, nil, cliEnv, false); err != nil {
			return err
		}
	}
	switch kind {
	case cuetry.StepKindCommand:
		return runCueStepCommand(out, recipe, execute, cliEnv, i, step, targets)
	case cuetry.StepKindPut:
		return runCueStepPut(out, recipe, recipeDir, execute, i, step, targets)
	case cuetry.StepKindGet:
		return runCueStepGet(out, recipe, recipeDir, execute, i, step, targets)
	case cuetry.StepKindScript:
		return runCueStepScript(out, recipeDir, recipe, execute, cliEnv, i, step, targets)
	case cuetry.StepKindPlugin:
		return runCueStepPluginDry(out, recipe, recipeDir, cliEnv, sshUser, secretResolver, pluginMgr, i, step, targets)
	case cuetry.StepKindTunnel:
		return runCueStepTunnelDry(out, recipe, i, step, targets)
	default:
		return nil
	}
}

func runCueStepCommand(out io.Writer, recipe cuetry.Recipe, execute bool, cliEnv map[string]string, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueKVTunnelDryLine(out, recipe, i, step, recipe.Defaults)
		WriteCueSSHPrivateKeyDryLine(out, i, step, recipe.Defaults)
		WriteCueStepHooksDryLines(out, i, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step, recipe.Defaults))
		for _, target := range targets {
			env, err := cuetry.EffectiveEnvForRunEx(context.Background(), false, nil, step, recipe.Defaults, cliEnv, &target, cueEnvRunOpts(&recipe, nil, nil, nil, true))
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			inner, err := cuetry.ShellExportPrefixForRemote(env, strings.TrimSpace(step.Command))
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}

			_, _ = fmt.Fprintf(out, "step %d: kind=command name=%q %s provider=%s run_as=%q remote=%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, runAs, remoteCmd)
		}
		return nil
	}
	return nil
}

func runCueStepPut(out io.Writer, recipe cuetry.Recipe, recipeDir string, execute bool, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Put.Local)
	if err != nil {
		return fmt.Errorf("step %d put.local: %w", i, err)
	}
	remotePath := strings.TrimSpace(step.Put.Remote)
	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step, recipe.Defaults))
		if _, statErr := os.Stat(localAbs); statErr != nil {
			_, _ = fmt.Fprintf(out, "step %d: kind=put (warning: local not readable: %v)\n", i, statErr)
		}
		for _, target := range targets {
			_, _ = fmt.Fprintf(out, "step %d: kind=put name=%q %s provider=%s %q → remote:%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, localAbs, remotePath)
		}
		return nil
	}
	return nil
}

func runCueStepGet(out io.Writer, recipe cuetry.Recipe, recipeDir string, execute bool, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	remotePath := strings.TrimSpace(step.Get.Remote)
	localRoot, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Get.Local)
	if err != nil {
		return fmt.Errorf("step %d get.local: %w", i, err)
	}
	if len(targets) > 1 {
		ok, err := cueGetLocalIsDirectory(step.Get.Local, localRoot)
		if err != nil {
			return fmt.Errorf("step %d get: %w", i, err)
		}
		if !ok {
			return fmt.Errorf("step %d get: %d hosts require get.local to be a directory (add trailing %q or use an existing directory); got %q",
				i, len(targets), string(filepath.Separator), step.Get.Local)
		}
	}
	jobs := make([]SFTPDownloadJob, 0, len(targets))
	base := filepath.Base(remotePath)
	if base == "." || base == "/" {
		base = "download"
	}
	for _, target := range targets {
		dest := localRoot
		if len(targets) > 1 {
			dest = filepath.Join(localRoot, cueSanitizeHostName(target.Name)+"_"+base)
		}
		jobs = append(jobs, SFTPDownloadJob{
			Record:     target,
			LocalAbs:   dest,
			RemotePath: remotePath,
		})
	}
	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step, recipe.Defaults))
		for _, j := range jobs {
			_, _ = fmt.Fprintf(out, "step %d: kind=get name=%q %s provider=%s remote:%q → %q\n",
				i, j.Record.Name, FormatTargetForDryRun(j.Record), j.Record.Provider, j.RemotePath, j.LocalAbs)
		}
		return nil
	}
	return nil
}

func runCueStepScript(out io.Writer, recipeDir string, recipe cuetry.Recipe, execute bool, cliEnv map[string]string, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Script.Local)
	if err != nil {
		return fmt.Errorf("step %d script.local: %w", i, err)
	}
	remotePath := strings.TrimSpace(step.Script.Remote)
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)

	if !execute {
		WriteCueStepNotifyDryLine(out, step)
		WriteCueKVTunnelDryLine(out, recipe, i, step, recipe.Defaults)
		WriteCueSSHPrivateKeyDryLine(out, i, step, recipe.Defaults)
		WriteCueStepHooksDryLines(out, i, step)
		WriteCueStepRetryDryLine(out, i, cuetry.EffectiveRetry(step, recipe.Defaults))
		if _, statErr := os.Stat(localAbs); statErr != nil {
			_, _ = fmt.Fprintf(out, "step %d: kind=script (warning: local not readable: %v)\n", i, statErr)
		}
		for _, target := range targets {
			env, err := cuetry.EffectiveEnvForRunEx(context.Background(), false, nil, step, recipe.Defaults, cliEnv, &target, cueEnvRunOpts(&recipe, nil, nil, nil, true))
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs, env)
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			_, _ = fmt.Fprintf(out, "step %d: kind=script name=%q %s provider=%s put %q → %q then exec run_as=%q cmd=%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, localAbs, remotePath, runAs, remoteCmd)
		}
		return nil
	}
	return nil
}

func agentTransferCloudFromRecipe(c *cuetry.RecipeAgentTransferCloud) AgentCloudBackend {
	if c == nil {
		return AgentCloudBackend{}
	}
	return AgentCloudBackend{
		Provider: strings.TrimSpace(c.Provider),
		Bucket:   strings.TrimSpace(c.Bucket),
		Prefix:   strings.TrimSpace(c.Prefix),
		Object:   strings.TrimSpace(c.Object),
		Region:   strings.TrimSpace(c.Region),
		Endpoint: strings.TrimSpace(c.Endpoint),
	}
}

func cloudBackendRefFromRecipe(r *cuetry.RecipeCloudBackendRef) *CloudBackendRef {
	if r == nil {
		return nil
	}
	return &CloudBackendRef{
		Kind:  strings.TrimSpace(r.Kind),
		Name:  strings.TrimSpace(r.Name),
		Index: r.Index,
	}
}

func summarizeAgentTransferEvents(events []AgentTransferEvent) string {
	var b strings.Builder
	for _, ev := range events {
		line := ev.Stage
		if ev.Host != "" {
			line = fmt.Sprintf("%s@%s", ev.Stage, ev.Host)
		}
		if strings.TrimSpace(ev.Message) != "" {
			line += ": " + strings.TrimSpace(ev.Message)
		}
		if !ev.Success && strings.TrimSpace(ev.Error) != "" {
			line += " (" + strings.TrimSpace(ev.Error) + ")"
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimSpace(line))
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "(no events)"
	}
	return s
}

func runCueStepAgentTransferDry(out io.Writer, records []hosts.Record, sshUser, configPath string, i int, step cuetry.RecipeStep) error {
	at := step.AgentTransfer
	if at == nil {
		return fmt.Errorf("step %d: internal: missing agent_transfer", i)
	}
	srcHosts, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	dstHosts, err := cuetry.ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return fmt.Errorf("step %d dest_host: %w", i, err)
	}
	if len(srcHosts) != 1 {
		return fmt.Errorf("step %d: agent_transfer requires exactly one source host; got %d", i, len(srcHosts))
	}
	if len(dstHosts) != 1 {
		return fmt.Errorf("step %d: agent_transfer requires exactly one destination host; got %d", i, len(dstHosts))
	}
	cloud := agentTransferCloudFromRecipe(at.Cloud)
	_, _ = fmt.Fprintf(out, "step %d: kind=agent_transfer ssh_user=%q\n", i, strings.TrimSpace(sshUser))
	_, _ = fmt.Fprintf(out, "  source: name=%q %s provider=%s path=%q\n",
		srcHosts[0].Name, FormatTargetForDryRun(srcHosts[0]), srcHosts[0].Provider, strings.TrimSpace(at.SourcePath))
	_, _ = fmt.Fprintf(out, "  dest:   name=%q %s provider=%s path=%q\n",
		dstHosts[0].Name, FormatTargetForDryRun(dstHosts[0]), dstHosts[0].Provider, strings.TrimSpace(at.DestPath))
	_, _ = fmt.Fprintf(out, "  cloud: provider=%q bucket=%q prefix=%q object=%q region=%q endpoint=%q\n",
		cloud.Provider, cloud.Bucket, cloud.Prefix, cloud.Object, cloud.Region, cloud.Endpoint)
	if at.CloudBackendRef != nil {
		_, _ = fmt.Fprintf(out, "  cloud_backend_ref: kind=%q name=%q index=%v\n",
			at.CloudBackendRef.Kind, at.CloudBackendRef.Name, at.CloudBackendRef.Index)
		if _, err := ResolveAgentTransferSigningHints(configPath, cloud, cloudBackendRefFromRecipe(at.CloudBackendRef)); err != nil {
			return fmt.Errorf("step %d signing hints: %w", i, err)
		}
		_, _ = fmt.Fprintln(out, "  signing hints: resolvable (config + ref)")
	} else {
		_, _ = fmt.Fprintln(out, "  cloud_backend_ref: (none — empty signing hints)")
	}
	if at.KeepObject {
		_, _ = fmt.Fprintln(out, "  keep_object: true")
	}
	if at.MaxRetries > 0 {
		_, _ = fmt.Fprintf(out, "  max_retries: %d\n", at.MaxRetries)
	}
	if ard := strings.TrimSpace(at.AgentRemoteDir); ard != "" {
		_, _ = fmt.Fprintf(out, "  agent_remote_dir: %q\n", ard)
	}
	WriteCueStepNotifyDryLine(out, step)
	return nil
}

func streamCueStepAgentTransfer(ctx context.Context, records []hosts.Record, sshUser, configPath string, i int, step cuetry.RecipeStep, cache *ClientCache) ([]HostExecResult, error) {
	at := step.AgentTransfer
	if at == nil {
		return nil, fmt.Errorf("step %d: internal: missing agent_transfer", i)
	}
	srcHosts, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}
	dstHosts, err := cuetry.ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return nil, fmt.Errorf("step %d dest_host: %w", i, err)
	}
	if len(srcHosts) != 1 || len(dstHosts) != 1 {
		msg := fmt.Sprintf("need exactly one source and one dest host; got src=%d dst=%d", len(srcHosts), len(dstHosts))
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | agent_transfer", i+1),
			Success:  false,
			ErrMsg:   msg,
			Provider: "local",
		}
		return []HostExecResult{res}, fmt.Errorf("step %d: %s", i, msg)
	}
	src := srcHosts[0]
	dst := dstHosts[0]
	cloud := agentTransferCloudFromRecipe(at.Cloud)
	ref := cloudBackendRefFromRecipe(at.CloudBackendRef)
	hints, err := ResolveAgentTransferSigningHints(configPath, cloud, ref)
	if err != nil {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | agent_transfer %s → %s", i+1, strings.TrimSpace(src.Name), strings.TrimSpace(dst.Name)),
			IP:       strings.TrimSpace(src.PrimaryIP),
			Provider: src.Provider,
			Success:  false,
			ErrMsg:   err.Error(),
		}
		return []HostExecResult{res}, fmt.Errorf("step %d: %w", i, err)
	}
	events, err := RunAgentTransferWithFallback(ctx, cache, sshUser, "", "", "", strings.TrimSpace(at.AgentRemoteDir),
		src, dst, strings.TrimSpace(at.SourcePath), strings.TrimSpace(at.DestPath),
		cloud, at.KeepObject, at.MaxRetries, hints,
		LoadTransferConfigFromConfigPath(configPath), nil)
	outStr := summarizeAgentTransferEvents(events)
	resName := fmt.Sprintf("agent_transfer %s → %s", strings.TrimSpace(src.Name), strings.TrimSpace(dst.Name))
	if err != nil {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | %s", i+1, resName),
			IP:       strings.TrimSpace(src.PrimaryIP),
			Provider: src.Provider,
			Success:  false,
			ErrMsg:   err.Error(),
			Output:   outStr,
		}
		return []HostExecResult{res}, fmt.Errorf("step %d agent_transfer: %w", i, err)
	}
	res := HostExecResult{
		Name:     fmt.Sprintf("Step %d | %s", i+1, resName),
		IP:       strings.TrimSpace(src.PrimaryIP),
		Provider: src.Provider,
		Success:  true,
		Output:   outStr,
	}
	return []HostExecResult{res}, nil
}

func cueGetLocalIsDirectory(localField, absResolved string) (bool, error) {
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

func cueSanitizeHostName(name string) string {
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
