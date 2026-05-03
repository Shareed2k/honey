package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/ui"
)

var (
	flagCueExecExecute bool
	flagCueExecEnv     []string

	cueExecCmd = &cobra.Command{
		Use:   "cue-exec <recipe.cue> [name]",
		Short: "Resolve a CUE recipe against search results and optionally run steps over SSH",
		Long: `Loads a .cue recipe (see examples/recipe), runs the same host search as honey search
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
(sudo -n on the remote run only); put/get SFTP uses the SSH login user.

Optional recipe.defaults.env and per-step env (map of NAME to value) set
export assignments before the shell command or sh <script> on the remote;
step keys override defaults. Not allowed on put/get steps.

Repeat -e/--env KEY=value to set remote variables from the CLI; they override
recipe env on duplicate keys (command and script steps only).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runCueExec,
	}
)

func init() {
	rootCmd.AddCommand(cueExecCmd)
	cueExecCmd.Flags().AddFlagSet(searchCmd.Flags())
	cueExecCmd.Flags().BoolVar(&flagCueExecExecute, "execute", false, "Run steps over SSH/SFTP (default: dry-run, print resolved plan only)")
	cueExecCmd.Flags().StringArrayVarP(&flagCueExecEnv, "env", "e", nil, "Remote env for command/script (repeat: -e KEY=value); overrides recipe env on duplicate keys")
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

	raw, err := safepath.ReadFile(absRecipe)
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

	recipe, err := cuetry.ParseRemoteRecipe(raw, records)
	if err != nil {
		return err
	}
	cliEnv, err := cuetry.ParseEnvKeyValuePairs(flagCueExecEnv)
	if err != nil {
		return err
	}

	if recipe.Defaults != nil && recipe.Defaults.K8sDebugImage != "" {
		for i := range records {
			if records[i].Provider == "k8s" && records[i].Meta["kind"] == "pod" {
				if records[i].Meta == nil {
					records[i].Meta = make(map[string]string)
				}
				records[i].Meta["debug_image"] = recipe.Defaults.K8sDebugImage
			}
		}
	}

	return ui.RunCueRecipeSteps(cmd.OutOrStdout(), recipe, recipeDir, records, sshUser, flagCueExecExecute, cliEnv)
}
