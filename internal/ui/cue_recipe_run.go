package ui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"honey/internal/cuetry"
	"honey/internal/hosts"
)

// RunCueRecipeSteps runs a parsed recipe against the given search snapshot (no
// second query). Writes the same plan / progress text as honey cue-exec.
func RunCueRecipeSteps(out io.Writer, recipe cuetry.Recipe, recipeDir string, records []hosts.Record, sshUser string, execute bool) error {
	if len(records) == 0 {
		return fmt.Errorf("no hosts in current result set")
	}
	for i, step := range recipe.Steps {
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
			runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
			remoteCmd, err := cuetry.WrapRemoteShell(runAs, step.Command)
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			if !execute {
				for _, target := range targets {
					_, _ = fmt.Fprintf(out, "step %d: kind=command name=%q ip=%s provider=%s run_as=%q remote=%q\n",
						i, target.Name, target.PrimaryIP, target.Provider, runAs, remoteCmd)
				}
				continue
			}
			res, err := ExecuteSSHParallel(sshUser, targets, remoteCmd, 0)
			if err != nil {
				return fmt.Errorf("step %d: ssh setup: %w", i, err)
			}
			if len(res) != len(targets) {
				return fmt.Errorf("step %d: expected %d ssh results, got %d", i, len(targets), len(res))
			}
			printCueHostExecResults(out, i, res)

		case cuetry.StepKindPut:
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
					_, _ = fmt.Fprintf(out, "step %d: kind=put name=%q ip=%s provider=%s %q → remote:%q\n",
						i, target.Name, target.PrimaryIP, target.Provider, localAbs, remotePath)
				}
				continue
			}
			if _, statErr := os.Stat(localAbs); statErr != nil {
				return fmt.Errorf("step %d put: local file %q: %w", i, localAbs, statErr)
			}
			res, err := ExecuteSFTPUploadParallel(sshUser, targets, localAbs, remotePath, 0)
			if err != nil {
				return fmt.Errorf("step %d: sftp upload setup: %w", i, err)
			}
			if len(res) != len(targets) {
				return fmt.Errorf("step %d: expected %d upload results, got %d", i, len(targets), len(res))
			}
			printCueHostExecResults(out, i, res)

		case cuetry.StepKindGet:
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
					_, _ = fmt.Fprintf(out, "step %d: kind=get name=%q ip=%s provider=%s remote:%q → %q\n",
						i, j.Record.Name, j.Record.PrimaryIP, j.Record.Provider, j.RemotePath, j.LocalAbs)
				}
				continue
			}
			if len(targets) > 1 {
				if err := os.MkdirAll(localRoot, 0o755); err != nil {
					return fmt.Errorf("step %d get: mkdir %q: %w", i, localRoot, err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(jobs[0].LocalAbs), 0o755); err != nil {
					return fmt.Errorf("step %d get: mkdir parent: %w", i, err)
				}
			}
			res, err := ExecuteSFTPDownloadParallel(sshUser, jobs, 0)
			if err != nil {
				return fmt.Errorf("step %d: sftp download setup: %w", i, err)
			}
			if len(res) != len(jobs) {
				return fmt.Errorf("step %d: expected %d download results, got %d", i, len(jobs), len(res))
			}
			printCueHostExecResults(out, i, res)

		case cuetry.StepKindScript:
			localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Script.Local)
			if err != nil {
				return fmt.Errorf("step %d script.local: %w", i, err)
			}
			remotePath := strings.TrimSpace(step.Script.Remote)
			runAs := cuetry.EffectiveRunAs(step, recipe.Defaults)
			remoteCmd, err := cuetry.ScriptRunAfterUpload(remotePath, runAs)
			if err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			if !execute {
				if _, statErr := os.Stat(localAbs); statErr != nil {
					_, _ = fmt.Fprintf(out, "step %d: kind=script (warning: local not readable: %v)\n", i, statErr)
				}
				for _, target := range targets {
					_, _ = fmt.Fprintf(out, "step %d: kind=script name=%q ip=%s provider=%s put %q → %q then exec run_as=%q cmd=%q\n",
						i, target.Name, target.PrimaryIP, target.Provider, localAbs, remotePath, runAs, remoteCmd)
				}
				continue
			}
			if _, statErr := os.Stat(localAbs); statErr != nil {
				return fmt.Errorf("step %d script: local file %q: %w", i, localAbs, statErr)
			}
			res, err := ExecuteScriptUploadRunParallel(sshUser, targets, localAbs, remotePath, remoteCmd, 0)
			if err != nil {
				return fmt.Errorf("step %d: script step setup: %w", i, err)
			}
			if len(res) != len(targets) {
				return fmt.Errorf("step %d: expected %d script results, got %d", i, len(targets), len(res))
			}
			printCueHostExecResults(out, i, res)
		}
	}

	if !execute {
		_, _ = fmt.Fprintln(out, "\nDry-run only. Append ! to the path in the TUI to execute, or use honey cue-exec --execute.")
	}
	return nil
}

func printCueHostExecResults(out io.Writer, stepIdx int, res []HostExecResult) {
	for _, r := range SortHostExecForUI(res) {
		status := "ok"
		if !r.Success {
			status = "FAILED"
		}
		_, _ = fmt.Fprintf(out, "step %d: [%s] %s @ %s — %s", stepIdx, r.Provider, r.Name, r.IP, status)
		if r.ErrMsg != "" {
			_, _ = fmt.Fprintf(out, " — %s", r.ErrMsg)
		}
		_, _ = fmt.Fprintln(out)
		if strings.TrimSpace(r.Output) != "" {
			_, _ = fmt.Fprintln(out, strings.TrimSpace(r.Output))
		}
	}
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
