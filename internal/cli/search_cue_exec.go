package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"hostctl/internal/cuetry"
	"hostctl/internal/ui"
)

var flagCueExecExecute bool

var cueExecCmd = &cobra.Command{
	Use:   "cue-exec <recipe.cue> [name]",
	Short: "Resolve a CUE recipe against search results and optionally run steps over SSH",
	Long: `Loads a .cue recipe (see examples/recipe), runs the same host search as hostctl search
(share all search flags), resolves each step's host field using search results:
literal IP, exact name match, host "*" for all rows with an IP, or host "re:PATTERN"
for a Go regexp (RE2) matched against each row's name (only rows with PrimaryIP).

Each step is exactly one of: shell command, put (upload), get (download), or
script (upload a local file then run it with sh on the same SSH connection).
Relative local paths are resolved against the recipe file's directory.

Then either prints a plan (--execute=false, default) or runs each step (--execute).

Optional positional name is forwarded like search: one extra argument sets the
name substring filter when --name / --name-regex are not set.

Use recipe.defaults.run_as or per-step run_as for command and script steps
(sudo -n on the remote run only); put/get SFTP uses the SSH login user.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runCueExec,
}

func init() {
	rootCmd.AddCommand(cueExecCmd)
	cueExecCmd.Flags().AddFlagSet(searchCmd.Flags())
	cueExecCmd.Flags().BoolVar(&flagCueExecExecute, "execute", false, "Run steps over SSH/SFTP (default: dry-run, print resolved plan only)")
}

func runCueExec(cmd *cobra.Command, args []string) error {
	recipePath := args[0]
	var queryArgs []string
	if len(args) > 1 {
		queryArgs = args[1:]
	}

	absRecipe, err := filepath.Abs(recipePath)
	if err != nil {
		return err
	}
	recipeDir := filepath.Dir(absRecipe)

	raw, err := os.ReadFile(recipePath)
	if err != nil {
		return err
	}
	recipe, err := cuetry.ParseRemoteRecipe(raw)
	if err != nil {
		return err
	}

	records, sshUser, err := runSearchCore(cmd, queryArgs)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return fmt.Errorf("search returned no hosts; widen filters or fix recipe host keys")
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
			if !flagCueExecExecute {
				for _, target := range targets {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "step %d: kind=command name=%q ip=%s provider=%s run_as=%q remote=%q\n",
						i, target.Name, target.PrimaryIP, target.Provider, runAs, remoteCmd)
				}
				continue
			}
			res, err := ui.ExecuteSSHParallel(sshUser, targets, remoteCmd, 0)
			if err != nil {
				return fmt.Errorf("step %d: ssh setup: %w", i, err)
			}
			if len(res) != len(targets) {
				return fmt.Errorf("step %d: expected %d ssh results, got %d", i, len(targets), len(res))
			}
			printHostExecResults(cmd, i, res)

		case cuetry.StepKindPut:
			localAbs, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Put.Local)
			if err != nil {
				return fmt.Errorf("step %d put.local: %w", i, err)
			}
			remotePath := strings.TrimSpace(step.Put.Remote)
			if !flagCueExecExecute {
				if _, statErr := os.Stat(localAbs); statErr != nil {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "step %d: kind=put (warning: local not readable: %v)\n", i, statErr)
				}
				for _, target := range targets {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "step %d: kind=put name=%q ip=%s provider=%s %q → remote:%q\n",
						i, target.Name, target.PrimaryIP, target.Provider, localAbs, remotePath)
				}
				continue
			}
			if _, statErr := os.Stat(localAbs); statErr != nil {
				return fmt.Errorf("step %d put: local file %q: %w", i, localAbs, statErr)
			}
			res, err := ui.ExecuteSFTPUploadParallel(sshUser, targets, localAbs, remotePath, 0)
			if err != nil {
				return fmt.Errorf("step %d: sftp upload setup: %w", i, err)
			}
			if len(res) != len(targets) {
				return fmt.Errorf("step %d: expected %d upload results, got %d", i, len(targets), len(res))
			}
			printHostExecResults(cmd, i, res)

		case cuetry.StepKindGet:
			remotePath := strings.TrimSpace(step.Get.Remote)
			localRoot, err := cuetry.ResolveLocalAgainstRecipe(recipeDir, step.Get.Local)
			if err != nil {
				return fmt.Errorf("step %d get.local: %w", i, err)
			}
			if len(targets) > 1 {
				ok, err := multiHostGetLocalIsDirectory(step.Get.Local, localRoot)
				if err != nil {
					return fmt.Errorf("step %d get: %w", i, err)
				}
				if !ok {
					return fmt.Errorf("step %d get: %d hosts require get.local to be a directory (add trailing %q or use an existing directory); got %q",
						i, len(targets), string(filepath.Separator), step.Get.Local)
				}
			}
			jobs := make([]ui.SFTPDownloadJob, 0, len(targets))
			base := filepath.Base(remotePath)
			if base == "." || base == "/" {
				base = "download"
			}
			for _, target := range targets {
				dest := localRoot
				if len(targets) > 1 {
					dest = filepath.Join(localRoot, sanitizeTransferHostName(target.Name)+"_"+base)
				}
				jobs = append(jobs, ui.SFTPDownloadJob{
					Record:     target,
					LocalAbs:   dest,
					RemotePath: remotePath,
				})
			}
			if !flagCueExecExecute {
				for _, j := range jobs {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "step %d: kind=get name=%q ip=%s provider=%s remote:%q → %q\n",
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
			res, err := ui.ExecuteSFTPDownloadParallel(sshUser, jobs, 0)
			if err != nil {
				return fmt.Errorf("step %d: sftp download setup: %w", i, err)
			}
			if len(res) != len(jobs) {
				return fmt.Errorf("step %d: expected %d download results, got %d", i, len(jobs), len(res))
			}
			printHostExecResults(cmd, i, res)

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
			if !flagCueExecExecute {
				if _, statErr := os.Stat(localAbs); statErr != nil {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "step %d: kind=script (warning: local not readable: %v)\n", i, statErr)
				}
				for _, target := range targets {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "step %d: kind=script name=%q ip=%s provider=%s put %q → %q then exec run_as=%q cmd=%q\n",
						i, target.Name, target.PrimaryIP, target.Provider, localAbs, remotePath, runAs, remoteCmd)
				}
				continue
			}
			if _, statErr := os.Stat(localAbs); statErr != nil {
				return fmt.Errorf("step %d script: local file %q: %w", i, localAbs, statErr)
			}
			res, err := ui.ExecuteScriptUploadRunParallel(sshUser, targets, localAbs, remotePath, remoteCmd, 0)
			if err != nil {
				return fmt.Errorf("step %d: script step setup: %w", i, err)
			}
			if len(res) != len(targets) {
				return fmt.Errorf("step %d: expected %d script results, got %d", i, len(targets), len(res))
			}
			printHostExecResults(cmd, i, res)
		}
	}

	if !flagCueExecExecute {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "\nDry-run only. Pass --execute to run over SSH/SFTP.")
	}
	return nil
}

func printHostExecResults(cmd *cobra.Command, stepIdx int, res []ui.HostExecResult) {
	for _, r := range ui.SortHostExecForUI(res) {
		status := "ok"
		if !r.Success {
			status = "FAILED"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "step %d: [%s] %s @ %s — %s", stepIdx, r.Provider, r.Name, r.IP, status)
		if r.ErrMsg != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), " — %s", r.ErrMsg)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout())
		if strings.TrimSpace(r.Output) != "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(r.Output))
		}
	}
}

func multiHostGetLocalIsDirectory(localField, absResolved string) (bool, error) {
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

func sanitizeTransferHostName(name string) string {
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
