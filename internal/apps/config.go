// Package apps provides structures and validation for configuring target
// proxy applications in honey.yaml.
package apps

import "time"

// AppType represents the protocol for the app proxy.
type AppType string

const (
	// AppTypeHTTP indicates an HTTP reverse proxy.
	AppTypeHTTP AppType = "http"
	// AppTypeTCP indicates a raw TCP proxy.
	AppTypeTCP AppType = "tcp"
)

// AppConfig defines a target application accessible via Honey proxy.
type AppConfig struct {
	Name        string        `yaml:"-" json:"name"`
	Type        AppType       `yaml:"type" json:"type"`
	Target      string        `yaml:"target,omitempty" json:"target,omitempty"`
	TargetRegex string        `yaml:"target_regex,omitempty" json:"target_regex,omitempty"`
	Backend     string        `yaml:"backend,omitempty" json:"backend,omitempty"`
	Provider    string        `yaml:"provider,omitempty" json:"provider,omitempty"`
	Upstream    string        `yaml:"upstream" json:"upstream"`
	LocalPort   int           `yaml:"local_port" json:"local_port"`
	TTL         time.Duration `yaml:"ttl" json:"ttl"`
	OpenBrowser bool          `yaml:"open_browser" json:"open_browser"`
}

// Config is a collection of apps indexed by name.
type Config map[string]AppConfig
