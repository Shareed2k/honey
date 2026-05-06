package webserver

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/safepath"
	"gopkg.in/yaml.v3"
)

// backupConfigIfExists writes cfgPath+".bak" with the previous file contents when cfgPath exists and is non-empty.
func backupConfigIfExists(cfgPath string) error {
	if cfgPath == "" {
		return nil
	}
	st, err := safepath.Stat(cfgPath)
	if err != nil || st.IsDir() {
		return nil
	}
	prev, err := safepath.ReadFile(cfgPath)
	if err != nil || len(prev) == 0 {
		return nil
	}
	return safepath.WriteFile(cfgPath+".bak", prev, 0o600)
}

// saveConfigFile marshals cfg to YAML, validates parse, backs up existing file, then writes cfgPath.
func saveConfigFile(cfgPath string, cfg *config.File) error {
	if cfg == nil {
		return safepath.WriteFile(cfgPath, nil, 0o600)
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	if _, err := config.ParseYAML(b); err != nil {
		return err
	}
	if err := backupConfigIfExists(cfgPath); err != nil {
		return err
	}
	return safepath.WriteFile(cfgPath, b, 0o600)
}
