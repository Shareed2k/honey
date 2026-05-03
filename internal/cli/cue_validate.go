package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
)

func init() {
	rootCmd.AddCommand(cueValidateCmd)
}

var cueValidateCmd = &cobra.Command{
	Use:   "cue-validate <file.cue>",
	Short: "Validate a CUE remote recipe (commands and/or SFTP put/get steps)",
	Long: `Parses a .cue file and checks that the top-level "recipe" field matches
the built-in schema: name (string) and steps (each step has host and exactly one
of command, put, get, or script {local, remote}; optional run_as on command/script steps).`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		b, err := safepath.ReadFile(args[0])
		if err != nil {
			return err
		}
		if err := cuetry.ValidateRemoteRecipe(b); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "OK: recipe satisfies honey remote-recipe schema")
		return nil
	},
}
