package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/shareed2k/honey/internal/config"
)

// LoadConfig reads the honey config from configDir/config.yaml and returns it as JSON.
// Returns a minimal empty-config JSON if the file does not exist (first-run case).
func LoadConfig(configDir string) (string, error) {
	path := filepath.Join(configDir, "config.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return `{"version":1,"defaults":{},"backends":{}}`, nil
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveConfig writes a JSON-encoded honey config to configDir/config.yaml.
// Creates the directory if it does not exist.
func SaveConfig(configDir string, configJSON string) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}
	var cfg config.File
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return err
	}
	return cfg.Save(filepath.Join(configDir, "config.yaml"))
}
