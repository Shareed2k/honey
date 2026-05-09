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
	"unicode"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// StreamCueRecipeSteps executes a CUE recipe step-by-step, streaming results.
// configPath is the resolved honey YAML path (may be empty); agent_transfer steps with cloud_backend_ref require it.
func StreamCueRecipeSteps(ctx context.Context, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, cliEnv map[string]string, configPath string, out chan<- HostExecResult) error {
	if len(records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	cache := NewClientCache()
	defer cache.CloseAll()

	for i, step := range recipe.Steps {
		if err := streamCueRecipeStep(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, i, step, out, cache); err != nil {
			return err
		}
	}
	return nil
}

// cueStepAllTargetsTransientTransportFailed reports whether every host result for
// the step looks like a transient SSH/transport failure (so continuing the recipe
// would likely repeat the same outage).
func cueStepAllTargetsTransientTransportFailed(results []HostExecResult) bool {
	if len(results) == 0 {
		return false
	}
	for _, r := range results {
		if r.Success {
			return false
		}
		if !IsSSHConnTransientError(errors.New(r.ErrMsg)) {
			return false
		}
	}
	return true
}

func streamCueRecipeStep(ctx context.Context, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, cliEnv map[string]string, configPath string, i int, step cuetry.RecipeStep, out chan<- HostExecResult, cache *ClientCache) error {
	kind, classifyErr := cuetry.ClassifyStep(step)
	if classifyErr != nil {
		return fmt.Errorf("step %d: %w", i, classifyErr)
	}
	if kind == cuetry.StepKindAgentTransfer {
		return streamCueStepAgentTransfer(ctx, records, sshUser, configPath, i, step, out, cache)
	}

	targets, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}

	// Fast path if nothing to run
	if len(targets) == 0 {
		return nil
	}

	// Create an intermediate channel to prefix the results with the step number
	ch := make(chan HostExecResult, len(targets))
	done := make(chan struct{})
	var stepResults []HostExecResult
	go func() {
		for res := range ch {
			stepResults = append(stepResults, res)
			res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
			out <- res
		}
		close(done)
	}()

	var stepErr error
	switch kind {
	case cuetry.StepKindCommand:
		stepErr = streamCueStepCommand(recipe, step, cliEnv, sshUser, targets, ch, cache)

	case cuetry.StepKindPut:
		stepErr = streamCueStepPut(recipeDir, step, sshUser, targets, ch, cache)

	case cuetry.StepKindGet:
		stepErr = streamCueStepGet(recipeDir, step, sshUser, targets, ch, cache)

	case cuetry.StepKindScript:
		stepErr = streamCueStepScript(recipe, recipeDir, step, cliEnv, sshUser, targets, ch, cache)
	}

	close(ch)
	<-done
	if stepErr != nil {
		return stepErr
	}
	if len(targets) > 0 && len(stepResults) == len(targets) && cueStepAllTargetsTransientTransportFailed(stepResults) {
		return fmt.Errorf("step %d: all %d targets failed with transient transport errors; aborting recipe", i+1, len(targets))
	}
	return nil
}

func streamCueStepCommand(recipe cuetry.Recipe, step cuetry.RecipeStep, cliEnv map[string]string, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, cache *ClientCache) error {
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)

	cmdFunc := func(r hosts.Record) string {
		env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv, &r)
		if err != nil {
			return fmt.Sprintf("echo 'env err: %s'", err.Error())
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

	return StreamSSHParallel(sshUser, targets, cmdFunc, 0, ch, cache)
}

func streamCueStepPut(recipeDir string, step cuetry.RecipeStep, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, cache *ClientCache) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Put.Local)
	if err != nil {
		return fmt.Errorf("put.local: %w", err)
	}
	remotePath := strings.TrimSpace(step.Put.Remote)
	if _, statErr := os.Stat(localAbs); statErr != nil {
		return fmt.Errorf("put: local file %q: %w", localAbs, statErr)
	}
	return StreamSFTPUploadParallel(sshUser, targets, localAbs, remotePath, 0, ch, cache)
}

func streamCueStepGet(recipeDir string, step cuetry.RecipeStep, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, cache *ClientCache) error {
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
	return StreamSFTPDownloadParallel(sshUser, jobs, 0, ch, cache)
}

func streamCueStepScript(recipe cuetry.Recipe, recipeDir string, step cuetry.RecipeStep, cliEnv map[string]string, sshUser string, targets []hosts.Record, ch chan<- HostExecResult, cache *ClientCache) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Script.Local)
	if err != nil {
		return fmt.Errorf("script.local: %w", err)
	}
	remotePath := strings.TrimSpace(step.Script.Remote)
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)

	if _, statErr := os.Stat(localAbs); statErr != nil {
		return fmt.Errorf("script: local file %q: %w", localAbs, statErr)
	}

	cmdFunc := func(r hosts.Record) string {
		env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv, &r)
		if err != nil {
			return fmt.Sprintf("echo 'env err: %s'", err.Error())
		}
		remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs, env)
		if err != nil {
			return fmt.Sprintf("echo 'wrap err: %s'", err.Error())
		}
		return remoteCmd
	}

	return StreamScriptUploadRunParallel(sshUser, targets, localAbs, remotePath, cmdFunc, 0, ch, cache)
}

// RunCueRecipeSteps executes a CUE recipe over a slice of target records without streaming.
// cliEnv is merged into each command/script step's remote env (overrides recipe env on duplicate keys); nil is treated as empty.
// configPath is the resolved honey YAML path (may be empty); agent_transfer with cloud_backend_ref requires it.
// rec, when non-nil, records a batch .hrec.jsonl (plan on dry-run, result rows on execute). Caller must Close(rec).
func RunCueRecipeSteps(ctx context.Context, out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string, configPath string, rec *SessionRecorder) error {
	if len(records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	if !execute {
		outWrite := out
		var capture bytes.Buffer
		if rec != nil {
			outWrite = io.MultiWriter(out, &capture)
		}
		for i, step := range recipe.Steps {
			if err := runCueRecipeStep(outWrite, recipe, recipeDir, records, sshUser, false, cliEnv, configPath, i, step); err != nil {
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
		return nil
	}

	// Second execution path: actual execution via streaming logic
	ch := make(chan HostExecResult)
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		errCh <- StreamCueRecipeSteps(ctx, recipe, recipeDir, records, sshUser, cliEnv, configPath, ch)
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
	}
	streamErr := <-errCh
	if streamErr != nil {
		if rec != nil {
			rec.RecordError(streamErr)
		}
		return streamErr
	}
	return nil
}

func runCueRecipeStep(out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string, configPath string, i int, step cuetry.RecipeStep) error {
	zap.L().Debug("evaluating cue step", zap.Int("step_index", i), zap.String("host", step.Host))
	kind, err := cuetry.ClassifyStep(step)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	if kind == cuetry.StepKindAgentTransfer {
		return runCueStepAgentTransferDry(out, records, sshUser, configPath, i, step)
	}
	targets, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	switch kind {
	case cuetry.StepKindCommand:
		return runCueStepCommand(out, recipe, execute, cliEnv, i, step, targets)
	case cuetry.StepKindPut:
		return runCueStepPut(out, recipeDir, execute, i, step, targets)
	case cuetry.StepKindGet:
		return runCueStepGet(out, recipeDir, execute, i, step, targets)
	case cuetry.StepKindScript:
		return runCueStepScript(out, recipeDir, recipe, execute, cliEnv, i, step, targets)
	default:
		return nil
	}
}

func runCueStepCommand(out io.Writer, recipe cuetry.Recipe, execute bool, cliEnv map[string]string, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	if !execute {
		for _, target := range targets {
			env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv, &target)
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

func runCueStepPut(out io.Writer, recipeDir string, execute bool, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Put.Local)
	if err != nil {
		return fmt.Errorf("step %d put.local: %w", i, err)
	}
	remotePath := strings.TrimSpace(step.Put.Remote)
	if !execute {
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

func runCueStepGet(out io.Writer, recipeDir string, execute bool, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
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
		if _, statErr := os.Stat(localAbs); statErr != nil {
			_, _ = fmt.Fprintf(out, "step %d: kind=script (warning: local not readable: %v)\n", i, statErr)
		}
		for _, target := range targets {
			env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv, &target)
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
	return nil
}

func streamCueStepAgentTransfer(ctx context.Context, records []hosts.Record, sshUser, configPath string, i int, step cuetry.RecipeStep, out chan<- HostExecResult, cache *ClientCache) error {
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
	if len(srcHosts) != 1 || len(dstHosts) != 1 {
		msg := fmt.Sprintf("need exactly one source and one dest host; got src=%d dst=%d", len(srcHosts), len(dstHosts))
		out <- HostExecResult{
			Name:    fmt.Sprintf("Step %d | agent_transfer", i+1),
			Success: false,
			ErrMsg:  msg,
		}
		return fmt.Errorf("step %d: %s", i, msg)
	}
	src := srcHosts[0]
	dst := dstHosts[0]
	cloud := agentTransferCloudFromRecipe(at.Cloud)
	ref := cloudBackendRefFromRecipe(at.CloudBackendRef)
	hints, err := ResolveAgentTransferSigningHints(configPath, cloud, ref)
	if err != nil {
		out <- HostExecResult{
			Name:     fmt.Sprintf("Step %d | agent_transfer %s → %s", i+1, strings.TrimSpace(src.Name), strings.TrimSpace(dst.Name)),
			IP:       strings.TrimSpace(src.PrimaryIP),
			Provider: src.Provider,
			Success:  false,
			ErrMsg:   err.Error(),
		}
		return fmt.Errorf("step %d: %w", i, err)
	}
	job, err := BuildAgentTransferJob(ctx, cache, sshUser, "", "", "", strings.TrimSpace(at.AgentRemoteDir),
		src, dst, strings.TrimSpace(at.SourcePath), strings.TrimSpace(at.DestPath),
		cloud, at.KeepObject, at.MaxRetries, hints)
	if err != nil {
		out <- HostExecResult{
			Name:     fmt.Sprintf("Step %d | agent_transfer %s → %s", i+1, strings.TrimSpace(src.Name), strings.TrimSpace(dst.Name)),
			IP:       strings.TrimSpace(src.PrimaryIP),
			Provider: src.Provider,
			Success:  false,
			ErrMsg:   err.Error(),
		}
		return fmt.Errorf("step %d: %w", i, err)
	}
	events, err := ExecuteAgentCloudTransfer(job, cache)
	outStr := summarizeAgentTransferEvents(events)
	resName := fmt.Sprintf("agent_transfer %s → %s", strings.TrimSpace(src.Name), strings.TrimSpace(dst.Name))
	if err != nil {
		out <- HostExecResult{
			Name:     fmt.Sprintf("Step %d | %s", i+1, resName),
			IP:       strings.TrimSpace(src.PrimaryIP),
			Provider: src.Provider,
			Success:  false,
			ErrMsg:   err.Error(),
			Output:   outStr,
		}
		return fmt.Errorf("step %d agent_transfer: %w", i, err)
	}
	out <- HostExecResult{
		Name:     fmt.Sprintf("Step %d | %s", i+1, resName),
		IP:       strings.TrimSpace(src.PrimaryIP),
		Provider: src.Provider,
		Success:  true,
		Output:   outStr,
	}
	return nil
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
