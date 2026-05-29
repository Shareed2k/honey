package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// applyCommandFlagDefaults reads defaults.<cmd.Name()>.* from the honey config via viper
// and sets each unset cobra flag from the matching config value.
// Config key "anomaly_model" maps to flag "anomaly-model" (underscore → hyphen).
// Zero / false / empty / nil values in the config are skipped so they never
// override the flag's built-in default.
func applyCommandFlagDefaults(cmd *cobra.Command, cfgPath string) {
	if cfgPath == "" {
		return
	}
	v := viper.New()
	v.SetConfigFile(cfgPath)
	if err := v.ReadInConfig(); err != nil {
		return // real config errors are surfaced by runSearchCore later
	}
	section := "defaults." + cmd.Name()
	for key, val := range v.GetStringMap(section) {
		if val == nil {
			continue
		}
		s := fmt.Sprintf("%v", val)
		if s == "" || s == "false" || s == "0" || s == "<nil>" {
			continue
		}
		flagName := strings.ReplaceAll(key, "_", "-")
		if cmd.Flags().Changed(flagName) {
			continue
		}
		if f := cmd.Flags().Lookup(flagName); f != nil {
			_ = f.Value.Set(s)
		}
	}
}
