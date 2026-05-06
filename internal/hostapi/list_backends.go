package hostapi

import (
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

// ListBackendsOutput matches MCP list_backends response shape.
type ListBackendsOutput struct {
	ConfigPath string              `json:"config_path"`
	Backends   []config.BackendRow `json:"backends"`
}

// ListBackends resolves config the same way as honey backends / MCP list_backends.
func ListBackends(configPath string) (ListBackendsOutput, error) {
	var out ListBackendsOutput
	cfgPath, err := config.ResolvePath(strings.TrimSpace(configPath))
	if err != nil {
		return out, err
	}
	if cfgPath == "" {
		return out, fmt.Errorf("no config file found (set config_path, HONEY_CONFIG, or install default config)")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return out, fmt.Errorf("config: %w", err)
	}
	out.ConfigPath = cfgPath
	out.Backends = searchrun.ListBackendRows(cfg)
	return out, nil
}
