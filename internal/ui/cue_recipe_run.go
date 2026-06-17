package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/engine"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	"go.uber.org/zap"
)

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

// transferConfigFromSessionHoney returns effective transfer config from loaded file or path.

// WriteCueSSHPrivateKeyDryLine prints one plan line when ssh_private_key is set for the step or defaults.
func WriteCueSSHPrivateKeyDryLine(out io.Writer, stepIdx int, step cuetry.Step, def *cuetry.RecipeDefaults) {
	key := cuetry.EffectiveSSHPrivateKey(def, engine.RemoteOpts(step))
	if key == "" {
		return
	}
	_, _ = fmt.Fprintf(out, "  step %d ssh_private_key: %q (exclusive — only this key file is used for SSH; no ssh_config IdentityFile, %s, or default ~/.ssh keys)\n",
		stepIdx, key, "HONEY_SSH_IDENTITY_FILES")
}

// StreamCueRecipeSteps executes a CUE recipe step-by-step, streaming results.

// cueStepAllTargetsTransientTransportFailed reports whether every host result for
// the step looks like a transient SSH/transport failure (so continuing the recipe
// would likely repeat the same outage).

func recipeHostMaxConc(step cuetry.Step, defaults *cuetry.RecipeDefaults) int {
	re := engine.RemoteOpts(step)
	if re != nil && re.Serial > 0 {
		return re.Serial
	}
	return cuetry.EffectiveMaxParallel(re, defaults)
}

// WriteCueKVTunnelDryLine prints one plan line when kv_tunnel is enabled for the step or defaults.
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

// RunCueRecipeSteps executes a CUE recipe over a slice of target records without streaming.
// cliEnv is merged into each command/script step's remote env (overrides recipe env on duplicate keys); nil is treated as empty.
// configPath is the resolved honey YAML path (may be empty); agent_transfer with cloud_backend_ref requires it.
// rec, when non-nil, records a batch .hrec.jsonl (plan on dry-run, result rows on execute). Caller must Close(rec).
func RunCueRecipeSteps(ctx context.Context, out io.Writer, p engine.CueRecipeRunParams, rec *engine.SessionRecorder) (runErr error) {
	if len(p.Records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	runStart := time.Now()
	if !p.Execute {
		defer func() { engine.ObserveRecipeRun(p.Obs, p.Recipe, false, runStart, runErr) }()
	}

	if !p.Execute {
		runErr = runCueRecipeStepsDry(out, p, rec)
		return runErr
	}
	runErr = runCueRecipeStepsExecute(ctx, out, p, rec)
	return runErr
}

func runCueRecipeStepsDry(out io.Writer, p engine.CueRecipeRunParams, rec *engine.SessionRecorder) error {
	var capture bytes.Buffer
	outWrite := io.Writer(&capture)
	if !p.JSON {
		if rec != nil {
			outWrite = io.MultiWriter(out, &capture)
		} else {
			outWrite = out
		}
	}
	mode, modeErr := cuetry.RecipeExecutionMode(p.Recipe)
	if modeErr != nil {
		return modeErr
	}
	if mode == cuetry.ExecutionModeGraph {
		text, err := cuetry.FormatGraphWavesText(p.Recipe)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(outWrite, text)
	}
	for i, ws := range p.Recipe.Steps {
		if i > 0 {
			_, _ = fmt.Fprintln(outWrite)
		}
		if err := runCueRecipeStep(outWrite, p.Recipe, p.RecipeDir, p.Records, p.SSHUser, false, p.CLIEnv, p.ConfigPath, i, ws.Step, p.SecretResolver, p.PluginMgr); err != nil {
			if rec != nil {
				rec.RecordError(err)
			}
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
	if p.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]string{"plan": capture.String()})
	}
	return nil
}

func runCueRecipeStepsExecute(ctx context.Context, out io.Writer, p engine.CueRecipeRunParams, rec *engine.SessionRecorder) error {
	// Second execution path: actual execution via streaming logic
	ch := make(chan engine.HostExecResult)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		errCh <- engine.StreamCueRecipeSteps(ctx, p, ch)
	}()

	results := []engine.HostExecResult{}
	lastStep := ""
	for res := range ch {
		if rec != nil {
			rec.RecordHostExecResult(res)
		}
		if p.JSON {
			results = append(results, res)
			continue
		}

		currentStep := ""
		if parts := strings.SplitN(res.Name, "|", 2); len(parts) > 0 {
			currentStep = strings.TrimSpace(parts[0])
		}
		if lastStep != "" && currentStep != lastStep {
			_, _ = fmt.Fprintln(out)
		}
		lastStep = currentStep

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
		if display := engine.CueRecipeDisplayOutput(res); strings.TrimSpace(display) != "" {
			_, _ = fmt.Fprintln(out, display)
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
	if streamErr != nil && rec != nil {
		rec.RecordError(streamErr)
	}

	if p.JSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string][]engine.HostExecResult{"results": results})
	}

	if streamErr != nil {
		return streamErr
	}
	return nil
}

func runCueRecipeStep(out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string, configPath string, i int, step cuetry.Step, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager) error {
	zap.L().Debug("evaluating cue step", zap.Int("step_index", i), zap.String("host", step.Base().Host))
	kind := step.Kind()
	var err error
	if kind == cuetry.KindAgentTransfer {
		return engine.RunCueStepAgentTransferDry(out, records, sshUser, configPath, i, step)
	}
	if kind == cuetry.KindAI {
		return engine.RunCueStepAIDry(out, recipe, execute, i, step)
	}
	if kind == cuetry.KindTemplate {
		return engine.RunCueStepTemplateDry(out, execute, i, step)
	}
	var targets []hosts.Record
	if engine.CueRecipeLoopUsesItemHost(step) && !execute {
		targets = []hosts.Record{{
			Provider:  "dynamic",
			Name:      "${item}",
			PrimaryIP: "${item}",
		}}
	} else {
		targets, err = cuetry.ExpandStepHosts(step.Base().Host, records)
		if err != nil {
			return fmt.Errorf("step %d: %w", i, err)
		}
	}
	targets = engine.CueApplyRecipeSSHDialOptions(recipe, engine.RemoteOpts(step), targets)
	if !execute && strings.TrimSpace(step.Base().When) != "" {
		if err := engine.WriteWhenDryLines(out, i, step, recipe, targets, nil, cliEnv, false); err != nil {
			return err
		}
	}
	switch kind {
	case cuetry.KindCommand:
		return engine.RunCueStepCommand(out, recipe, execute, cliEnv, i, step, targets)
	case cuetry.KindPut:
		return engine.RunCueStepPut(out, recipe, recipeDir, execute, i, step, targets)
	case cuetry.KindGet:
		return engine.RunCueStepGet(out, recipe, recipeDir, execute, i, step, targets)
	case cuetry.KindScript:
		return engine.RunCueStepScript(out, recipeDir, recipe, execute, cliEnv, i, step, targets)
	case cuetry.KindPlugin:
		return engine.RunCueStepPluginDry(out, recipe, recipeDir, cliEnv, sshUser, secretResolver, pluginMgr, i, step, targets)
	case cuetry.KindTunnel:
		return engine.RunCueStepTunnelDry(out, recipe, i, step, targets)
	case cuetry.KindDocker:
		return engine.RunCueStepDockerDry(out, recipe, i, step, targets)
	default:
		return nil
	}
}
