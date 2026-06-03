package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/recordings"
	"github.com/shareed2k/honey/internal/safepath"
	"github.com/shareed2k/honey/internal/ui"
)

var (
	flagCueExecExecute bool
	flagCueExecEnv     []string
	flagRetryFailed    string

	cueExecCmd = &cobra.Command{
		Use:   "cue-exec <recipe.cue> [name]",
		Short: "Resolve a CUE recipe against search results and optionally run steps over SSH",
		Long: `Loads a .cue recipe (see examples/recipe), runs the same host search as honey search
(share all search flags), resolves each step's host field using search results:
literal IP, exact name match, host "*" for all rows with an IP, or host "re:PATTERN"
for a Go regexp (RE2) matched against each row's name (only rows with PrimaryIP).

Each step is exactly one of: shell command, put (upload), get (download),
script (upload a local file then run it with sh on the same SSH connection),
agent_transfer (A→cloud→B using the transfer agent; requires --config when using cloud_backend_ref),
or ai (terminal local summarizer after prior steps; host must be "_"; OPENAI_API_KEY when executing).
Relative local paths are resolved against the recipe file's directory.

Then either prints a plan (--execute=false, default) or runs each step (--execute).

Optional positional name is forwarded like search: one extra argument sets the
name substring filter when --name / --name-regex are not set.

Use recipe.defaults.run_as or per-step run_as for command and script steps
(sudo -n on the remote run only); put/get SFTP uses the SSH login user.

Optional recipe.defaults.env and per-step env (map of NAME to value) set
export assignments before the shell command or sh <script> on the remote;
step keys override defaults. Optional defaults.secrets and step secrets (command/script
only) map NAME to ref strings (env:VAR, keyring://…, etc.) resolved at execute time; dry-run
shows redacted placeholders, not resolved values. Not allowed on put/get/ai steps.

Repeat -e/--env KEY=value to set remote variables from the CLI; they override
recipe env on duplicate keys (command and script steps only).

With global --record-dir or defaults.record_dir in config, writes one batch .hrec.jsonl per
invocation when recording is enabled: explicit --record-dir, or record_dir set in honey YAML
(built-in default records/ alone does not enable cue-exec batch logs). Dry-run records the plan text; --execute records each step result.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runCueExec,
	}
)

func init() {
	rootCmd.AddCommand(cueExecCmd)
	cueExecCmd.Flags().AddFlagSet(searchCmd.Flags())
	cueExecCmd.Flags().BoolVar(&flagCueExecExecute, "execute", false, "Run steps over SSH/SFTP (default: dry-run, print resolved plan only)")
	cueExecCmd.Flags().StringArrayVarP(&flagCueExecEnv, "env", "e", nil, "Remote env for command/script (repeat: -e KEY=value); overrides recipe env on duplicate keys")
	cueExecCmd.Flags().StringVar(&flagRetryFailed, "retry-failed", "", "Re-run only hosts that did not succeed in this recording (basename, e.g. 20260529_….hrec.jsonl)")
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
	records, sshUser, cfg, cfgPath, err := runSearchCore(cmd, queryArgs)
	if err != nil {
		return err
	}

	if flagRetryFailed != "" {
		dir := resolveRecordingsDir(cmd)
		prevEvents, err := recordings.LoadEvents(dir, flagRetryFailed)
		if err != nil {
			return fmt.Errorf("--retry-failed: %w", err)
		}
		succeeded := recordings.SucceededHosts(prevEvents)
		filtered := records[:0]
		for _, r := range records {
			if _, ok := succeeded[r.Name+"@"+r.PrimaryIP]; !ok {
				filtered = append(filtered, r)
			}
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "retry-failed: %d succeeded host(s) skipped, retrying %d\n",
			len(succeeded), len(filtered))
		records = filtered
	}

	if len(records) == 0 {
		return fmt.Errorf("search returned no hosts; widen filters or fix recipe host keys")
	}

	pluginMgr, err := plugins.Open(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer func() { _ = pluginMgr.Close() }()

	recipe, err := cuetry.ParseRemoteRecipeOpts(raw, records, cuetry.ParseOptions{PluginManager: pluginMgr})
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

	recordDir := config.ResolveRecordDir(cfg, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
	flagSet := recordDirFlagChanged(cmd) && strings.TrimSpace(flagRecordDir) != ""
	yamlSet := cfg != nil && strings.TrimSpace(cfg.Defaults.RecordDir) != "" && !recordDirFlagChanged(cmd)
	wantBatch := len(records) > 0 && (flagSet || yamlSet)
	var rec *ui.SessionRecorder
	if wantBatch {
		trigger := "cli-cue-exec-dry"
		if flagCueExecExecute {
			trigger = "cli-cue-exec"
		}
		var err error
		rec, err = ui.NewBatchSessionRecorder(recordDir, trigger, sshUser, len(records))
		if err != nil {
			return err
		}
		if rec != nil {
			hash, _ := cuetry.HashRecipeJSON(recipe)
			rec.RecordRecipeMeta(ui.RecipeMeta{
				RecipePath:        absRecipe,
				HostCount:         len(records),
				RecipeContentHash: hash,
				StartedAt:         time.Now().UTC(),
				Hosts:             ui.HostsForRecipeMeta(records, 200),
			})
		}
		defer func() { _ = rec.Close() }()
	}
	aiPrompt := ""
	if cfg != nil {
		aiPrompt = strings.TrimSpace(cfg.Defaults.AISystemPrompt)
	}
	secRes, err := cuetry.NewSecretResolverWithPlugins(cuetry.SecretResolverOptionsFromHoney(cfg), pluginMgr)
	if err != nil {
		return err
	}
	return ui.RunCueRecipeSteps(context.Background(), cmd.OutOrStdout(), ui.CueRecipeRunParams{
		Recipe:         recipe,
		RecipeDir:      recipeDir,
		Records:        records,
		SSHUser:        sshUser,
		CLIEnv:         cliEnv,
		ConfigPath:     cfgPath,
		AISystemPrompt: aiPrompt,
		SecretResolver: secRes,
		PluginMgr:      pluginMgr,
		Execute:        flagCueExecExecute,
		Reg:            buildHostExecRegistry(),
	}, rec)
}
