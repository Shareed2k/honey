package ui

import (
	"fmt"
	"honey/internal/cuetry"
	"honey/internal/hosts"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func StreamCueRecipeSteps(recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, cliEnv map[string]string, out chan<- HostExecResult) error {
	if len(records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	cache := NewClientCache()
	defer cache.CloseAll()

	for i, step := range recipe.Steps {
		if err := streamCueRecipeStep(recipe, recipeDir, records, sshUser, cliEnv, i, step, out, cache); err != nil {
			return err
		}
	}
	return nil
}

func streamCueRecipeStep(recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, cliEnv map[string]string, i int, step cuetry.RecipeStep, out chan<- HostExecResult, cache *ClientCache) error {
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
	go func() {
		for res := range ch {
			res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
			out <- res
		}
		close(done)
	}()

	var stepErr error
	kind, err := cuetry.ClassifyStep(step)
	if err != nil {
		stepErr = fmt.Errorf("step %d: %w", i, err)
	} else {
		switch kind {
		case cuetry.StepKindCommand:
			runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
			env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv)
			if err != nil {
				stepErr = fmt.Errorf("step %d: %w", i, err)
			} else {
				inner, err := cuetry.ShellExportPrefixForRemote(env, strings.TrimSpace(step.Command))
				if err != nil {
					stepErr = fmt.Errorf("step %d: %w", i, err)
				} else {
					remoteCmd, err := cuetry.WrapRemoteShell(runAs, inner)
					if err != nil {
						stepErr = fmt.Errorf("step %d: %w", i, err)
					} else {
						stepErr = StreamSSHParallel(sshUser, targets, remoteCmd, 0, ch, cache)
					}
				}
			}

		case cuetry.StepKindPut:
			localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Put.Local)
			if err != nil {
				stepErr = fmt.Errorf("step %d put.local: %w", i, err)
			} else {
				remotePath := strings.TrimSpace(step.Put.Remote)
				if _, statErr := os.Stat(localAbs); statErr != nil {
					stepErr = fmt.Errorf("step %d put: local file %q: %w", i, localAbs, statErr)
				} else {
					stepErr = StreamSFTPUploadParallel(sshUser, targets, localAbs, remotePath, 0, ch, cache)
				}
			}

		case cuetry.StepKindGet:
			remotePath := strings.TrimSpace(step.Get.Remote)
			localRoot, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Get.Local)
			if err != nil {
				stepErr = fmt.Errorf("step %d get.local: %w", i, err)
			} else {
				if len(targets) > 1 {
					ok, err := cueGetLocalIsDirectory(step.Get.Local, localRoot)
					if err != nil {
						stepErr = fmt.Errorf("step %d get: %w", i, err)
					} else if !ok {
						stepErr = fmt.Errorf("step %d get: %d hosts require get.local to be a directory; got %q", i, len(targets), step.Get.Local)
					}
				}
				if stepErr == nil {
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
							stepErr = fmt.Errorf("step %d get: mkdir %q: %w", i, localRoot, err)
						}
					} else {
						if err := os.MkdirAll(filepath.Dir(jobs[0].LocalAbs), 0o750); err != nil {
							stepErr = fmt.Errorf("step %d get: mkdir parent: %w", i, err)
						}
					}
					if stepErr == nil {
						stepErr = StreamSFTPDownloadParallel(sshUser, jobs, 0, ch, cache)
					}
				}
			}

		case cuetry.StepKindScript:
			localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Script.Local)
			if err != nil {
				stepErr = fmt.Errorf("step %d script.local: %w", i, err)
			} else {
				remotePath := strings.TrimSpace(step.Script.Remote)
				runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
				env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv)
				if err != nil {
					stepErr = fmt.Errorf("step %d: %w", i, err)
				} else {
					remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs, env)
					if err != nil {
						stepErr = fmt.Errorf("step %d: %w", i, err)
					} else {
						if _, statErr := os.Stat(localAbs); statErr != nil {
							stepErr = fmt.Errorf("step %d script: local file %q: %w", i, localAbs, statErr)
						} else {
							stepErr = StreamScriptUploadRunParallel(sshUser, targets, localAbs, remotePath, remoteCmd, 0, ch, cache)
						}
					}
				}
			}
		}
	}

	close(ch)
	<-done
	return stepErr
}
// second query). Writes the same plan / progress text as honey cue-exec.
// cliEnv is merged into each command/script step's remote env (overrides recipe env on duplicate keys); nil is treated as empty.
func RunCueRecipeSteps(out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string) error {
	if len(records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}

	if !execute {
		for i, step := range recipe.Steps {
			if err := runCueRecipeStep(out, recipe, recipeDir, records, sshUser, false, cliEnv, i, step); err != nil {
				return err
			}
		}
		_, _ = fmt.Fprintln(out, "\nDry-run only. Append ! to the path in the TUI to execute, or use honey cue-exec --execute.")
		return nil
	}

	// Second execution path: actual execution via streaming logic
	ch := make(chan HostExecResult)
	go func() {
		defer close(ch)
		_ = StreamCueRecipeSteps(recipe, recipeDir, records, sshUser, cliEnv, ch)
	}()

	for res := range ch {
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
	return nil
}

func runCueRecipeStep(out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool, cliEnv map[string]string, i int, step cuetry.RecipeStep) error {
	targets, err := cuetry.ExpandStepHosts(step.Host, records)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	kind, err := cuetry.ClassifyStep(step)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	switch kind {
	case cuetry.StepKindCommand:
		return runCueStepCommand(out, recipe, sshUser, execute, cliEnv, i, step, targets)
	case cuetry.StepKindPut:
		return runCueStepPut(out, recipeDir, sshUser, execute, i, step, targets)
	case cuetry.StepKindGet:
		return runCueStepGet(out, recipeDir, sshUser, execute, i, step, targets)
	case cuetry.StepKindScript:
		return runCueStepScript(out, recipeDir, recipe, sshUser, execute, cliEnv, i, step, targets)
	default:
		return nil
	}
}

func runCueStepCommand(out io.Writer, recipe cuetry.Recipe, sshUser string, execute bool, cliEnv map[string]string, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv)
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
	if !execute {
		for _, target := range targets {
			_, _ = fmt.Fprintf(out, "step %d: kind=command name=%q %s provider=%s run_as=%q remote=%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, runAs, remoteCmd)
		}
		return nil
	}
	return nil
}

func runCueStepPut(out io.Writer, recipeDir, sshUser string, execute bool, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
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

func runCueStepGet(out io.Writer, recipeDir, sshUser string, execute bool, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
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

func runCueStepScript(out io.Writer, recipeDir string, recipe cuetry.Recipe, sshUser string, execute bool, cliEnv map[string]string, i int, step cuetry.RecipeStep, targets []hosts.Record) error {
	localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Script.Local)
	if err != nil {
		return fmt.Errorf("step %d script.local: %w", i, err)
	}
	remotePath := strings.TrimSpace(step.Script.Remote)
	runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
	env, err := cuetry.EffectiveEnvForRun(step, recipe.Defaults, cliEnv)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs, env)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	if !execute {
		if _, statErr := os.Stat(localAbs); statErr != nil {
			_, _ = fmt.Fprintf(out, "step %d: kind=script (warning: local not readable: %v)\n", i, statErr)
		}
		for _, target := range targets {
			_, _ = fmt.Fprintf(out, "step %d: kind=script name=%q %s provider=%s put %q → %q then exec run_as=%q cmd=%q\n",
				i, target.Name, FormatTargetForDryRun(target), target.Provider, localAbs, remotePath, runAs, remoteCmd)
		}
		return nil
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
