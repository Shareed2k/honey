package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/config"
)

// InitDefaultConfig initializes the config.yaml file with default paths if they are not set.
func InitDefaultConfig(homeDir, configDir, cacheDir, recordDir, recipesDir string) error {
	_ = os.Setenv("HOME", homeDir)

	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return err
	}

	path := filepath.Join(configDir, "config.yaml")
	var cfg *config.File
	var err error

	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		cfg = &config.File{Version: 1}
	} else {
		cfg, err = config.Load(path)
		if err != nil {
			return err
		}
	}

	modified := false
	if cfg.Defaults.CacheDir == "" {
		cfg.Defaults.CacheDir = cacheDir
		modified = true
	}
	if cfg.Defaults.RecordDir == "" {
		cfg.Defaults.RecordDir = recordDir
		modified = true
	}
	if cfg.Defaults.Studio.RecipesPath == "" {
		cfg.Defaults.Studio.RecipesPath = recipesDir
		modified = true
	}

	if modified {
		return cfg.Save(path)
	}
	return nil
}

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

// defaultSSHUser returns Defaults.SSHUser from the config at configPath, or ""
// if unset/unreadable. Mirrors the desktop fallback used when no SSH user is
// given (internal/cli/search.go).
func defaultSSHUser(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return ""
	}
	cfg, err := config.Load(configPath)
	if err != nil || cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Defaults.SSHUser)
}
