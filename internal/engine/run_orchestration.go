package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	"go.uber.org/zap"
)

// RunCueRecipeSteps executes a CUE recipe over a slice of target records.
func RunCueRecipeSteps(ctx context.Context, out io.Writer, p CueRecipeRunParams, rec *SessionRecorder) (runErr error) {
	if len(p.Records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	runStart := time.Now()
	if !p.Execute {
		defer func() { ObserveRecipeRun(p.Obs, p.Recipe, false, runStart, runErr) }()
	}

	if !p.Execute {
		runErr = runCueRecipeStepsDry(out, p, rec)
		return runErr
	}
	runErr = runCueRecipeStepsExecute(ctx, out, p, rec)
	return runErr
}

func runCueRecipeStepsDry(out io.Writer, p CueRecipeRunParams, rec *SessionRecorder) error {
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

func runCueRecipeStepsExecute(ctx context.Context, out io.Writer, p CueRecipeRunParams, rec *SessionRecorder) error {
	// Second execution path: actual execution via streaming logic
	ch := make(chan HostExecResult)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		errCh <- StreamCueRecipeSteps(ctx, p, ch)
	}()

	results := []HostExecResult{}
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
		if display := CueRecipeDisplayOutput(res); strings.TrimSpace(display) != "" {
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
		_ = enc.Encode(map[string][]HostExecResult{"results": results})
	}

	if streamErr != nil {
		return streamErr
	}
	return nil
}

func runCueRecipeStep(out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string, configPath string, i int, step cuetry.Step, secretResolver cuetry.SecretResolver, pluginMgr *plugins.Manager) error {
	zap.L().Debug("evaluating cue step", zap.Int("step_index", i), zap.String("host", step.Base().Host))
	kind := step.Kind()
	var targets []hosts.Record
	if CueRecipeLoopUsesItemHost(step) && !execute {
		targets = []hosts.Record{{
			Provider:  "dynamic",
			Name:      "${item}",
			PrimaryIP: "${item}",
		}}
	} else {
		// Some steps (like AI) might not have a host field and will fail to expand.
		// That's fine, we pass the targets anyway and let the Executor handle the error if it needs to.
		targets, _ = cuetry.ExpandStepHosts(step.Base().Host, records)
	}

	targets = CueApplyRecipeSSHDialOptions(recipe, RemoteOpts(step), targets)
	if !execute && strings.TrimSpace(step.Base().When) != "" && len(targets) > 0 {
		if err := WriteWhenDryLines(out, i, step, recipe, targets, nil, cliEnv, false); err != nil {
			return err
		}
	}

	// Dry-run builds a lightweight CueRun so StepContext.Run is always set — the
	// single source of run-scoped inputs for executors. Run-state fields
	// (OutputStore, KV, …) stay nil; dry-run executors don't use them.
	run := &CueRun{Params: CueRecipeRunParams{
		Recipe:         recipe,
		RecipeDir:      recipeDir,
		Records:        records,
		SSHUser:        sshUser,
		Execute:        execute,
		CLIEnv:         cliEnv,
		ConfigPath:     configPath,
		SecretResolver: secretResolver,
		PluginMgr:      pluginMgr,
	}}

	var targetCtxs []TargetContext
	for _, t := range targets {
		// Just provide empty env for dry run if not doing full resolution?
		// Wait, `StepEnv` can be called here too for dry run.
		env, _ := run.StepEnv(context.Background(), step.Base(), &t, false, true) // dryRun = true
		targetCtxs = append(targetCtxs, TargetContext{Record: t, Env: env})
	}

	sc := &StepContext{
		Ctx:      context.Background(),
		Run:      run,
		Out:      out,
		Targets:  targetCtxs,
		Index:    i,
		Step:     step,
		Kind:     kind,
		ResultCh: nil, // dry-run doesn't stream results this way
	}

	exec, err := GetStepExecutor(kind)
	if err == nil {
		return exec.ExecuteDryRun(sc)
	}

	return fmt.Errorf("step %d: unsupported step kind for dry run: %q", i, kind)
}
