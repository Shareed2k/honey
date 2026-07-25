package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/safepath"
)

// Plugins configures WASM plugins loaded from disk (Extism).
type Plugins struct {
	Enabled           *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Directory         string   `yaml:"directory,omitempty" json:"directory,omitempty" mod:"trim"`
	Allowlist         []string `yaml:"allowlist,omitempty" json:"allowlist,omitempty"`
	MaxMemoryMB       int      `yaml:"max_memory_mb,omitempty" json:"max_memory_mb,omitempty"`
	TimeoutMS         int      `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	NetworkDeny       *bool    `yaml:"network_deny,omitempty" json:"network_deny,omitempty"`
	NetworkAllowHosts []string `yaml:"network_allow_hosts,omitempty" json:"network_allow_hosts,omitempty"`
	AllowHostNetwork  bool     `yaml:"allow_host_network,omitempty" json:"allow_host_network,omitempty"`
}

// PluginsEffective holds resolved plugin settings for runtime.
type PluginsEffective struct {
	Enabled           bool
	Directory         string
	Allowlist         []string
	MaxMemoryMB       int
	TimeoutMS         int
	NetworkDeny       bool
	NetworkAllowHosts []string
	AllowHostNetwork  bool
}

const (
	defaultPluginsMaxMemoryMB = 32
	defaultPluginsTimeoutMS   = 30000
)

// WithDefaults returns effective plugin settings (plugins disabled unless explicitly enabled).
func (p Plugins) WithDefaults() PluginsEffective {
	e := PluginsEffective{
		Enabled:     false,
		MaxMemoryMB: defaultPluginsMaxMemoryMB,
		TimeoutMS:   defaultPluginsTimeoutMS,
		NetworkDeny: true, // secure-by-default: plugins cannot make arbitrary network calls
	}
	if p.Enabled != nil {
		e.Enabled = *p.Enabled
	}
	if p.Directory != "" {
		e.Directory = p.Directory
	} else {
		e.Directory = DefaultPluginsDir()
	}
	if len(p.Allowlist) > 0 {
		e.Allowlist = append([]string(nil), p.Allowlist...)
	}
	if p.MaxMemoryMB > 0 {
		e.MaxMemoryMB = p.MaxMemoryMB
	}
	if p.TimeoutMS > 0 {
		e.TimeoutMS = p.TimeoutMS
	}
	if p.NetworkDeny != nil {
		e.NetworkDeny = *p.NetworkDeny
	}
	if len(p.NetworkAllowHosts) > 0 {
		e.NetworkAllowHosts = append([]string(nil), p.NetworkAllowHosts...)
	}
	e.AllowHostNetwork = p.AllowHostNetwork
	return e
}

// DefaultPluginsDir returns ~/.config/honey/plugins (or XDG_CONFIG_HOME/honey/plugins).
func DefaultPluginsDir() string {
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		if p, err := safepath.JoinUnder(base, "honey", "plugins"); err == nil {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "honey", "plugins")
	}
	if p, err := safepath.JoinUnder(home, ".config", "honey", "plugins"); err == nil {
		return p
	}
	return filepath.Join(home, ".config", "honey", "plugins")
}
