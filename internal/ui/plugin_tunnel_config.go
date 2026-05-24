package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

type pluginTunnelConfig struct {
	TunnelStep string `json:"tunnel_step"`
	BaseURL    string `json:"base_url"`
}

// RewritePluginConfigTunnelStep sets base_url from a recipe tunnel step endpoint.
// When pluginID is rclone, tunnel_step or a non-empty base_url is required on execute.
func RewritePluginConfigTunnelStep(config []byte, pluginID string, tunnelCoord *RecipeTunnelCoordinator, sshUser string, target hosts.Record, execute bool) ([]byte, error) {
	if len(config) == 0 {
		if strings.TrimSpace(pluginID) == "rclone" {
			return nil, fmt.Errorf("rclone plugin config is required")
		}
		return config, nil
	}
	var cfg pluginTunnelConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("plugin config: %w", err)
	}
	ts := strings.TrimSpace(cfg.TunnelStep)
	base := strings.TrimSpace(cfg.BaseURL)
	if ts == "" {
		if strings.TrimSpace(pluginID) == "rclone" && base == "" && execute {
			return nil, fmt.Errorf("rclone plugin requires tunnel_step (recipe tunnel step id)")
		}
		return config, nil
	}
	if tunnelCoord == nil {
		if !execute {
			return config, nil
		}
		return nil, fmt.Errorf("tunnel_step %q requires an active recipe tunnel coordinator", ts)
	}
	host, port, ok := tunnelCoord.LookupEndpoint(ts, sshUser, target)
	if !ok {
		if !execute {
			return config, nil
		}
		return nil, fmt.Errorf("tunnel_step %q not found for host %q", ts, target.Name)
	}
	if base == "" {
		base = fmt.Sprintf("http://%s:%d", host, port)
	}
	var root map[string]any
	if err := json.Unmarshal(config, &root); err != nil {
		return nil, fmt.Errorf("plugin config: %w", err)
	}
	root["base_url"] = base
	root["tunnel_step"] = ts
	return json.Marshal(root)
}
