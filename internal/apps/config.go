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
	// AppTypeRecipe indicates a shortcut to a CUE recipe execution.
	AppTypeRecipe AppType = "recipe"
)

// AppConfig defines a target application accessible via Honey proxy.
type AppConfig struct {
	Name                string            `yaml:"-" json:"name"`
	Type                AppType           `yaml:"type" json:"type"`
	Mode                string            `yaml:"mode,omitempty" json:"mode,omitempty"`
	Target              string            `yaml:"target,omitempty" json:"target,omitempty"`
	TargetRegex         string            `yaml:"target_regex,omitempty" json:"target_regex,omitempty"`
	TargetRecipe        string            `yaml:"target_recipe,omitempty" json:"target_recipe,omitempty"`
	Backend             string            `yaml:"backend,omitempty" json:"backend,omitempty"`
	Provider            string            `yaml:"provider,omitempty" json:"provider,omitempty"`
	Upstream            string            `yaml:"upstream" json:"upstream" validate:"required,url"`
	LocalPort           int               `yaml:"local_port" json:"local_port"`
	TTL                 time.Duration     `yaml:"ttl" json:"ttl"`
	OpenBrowser         bool              `yaml:"open_browser" json:"open_browser"`
	PassHostHeader      bool              `yaml:"pass_host_header" json:"pass_host_header"`
	TrustForwardHeaders bool              `yaml:"trust_forward_headers" json:"trust_forward_headers"`
	InsecureSkipVerify  bool              `yaml:"insecure_skip_verify" json:"insecure_skip_verify"`
	Headers             map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	CORS                *CORSConfig       `yaml:"cors,omitempty" json:"cors,omitempty"`
	Webhooks            []string          `yaml:"-" json:"webhooks,omitempty"`
}

// CORSConfig defines the CORS policy for an application.
type CORSConfig struct {
	AllowedOrigins   []string `yaml:"allowed_origins,omitempty" json:"allowed_origins,omitempty"`
	AllowedMethods   []string `yaml:"allowed_methods,omitempty" json:"allowed_methods,omitempty"`
	AllowedHeaders   []string `yaml:"allowed_headers,omitempty" json:"allowed_headers,omitempty"`
	ExposedHeaders   []string `yaml:"exposed_headers,omitempty" json:"exposed_headers,omitempty"`
	AllowCredentials bool     `yaml:"allow_credentials" json:"allow_credentials"`
	MaxAge           int      `yaml:"max_age" json:"max_age"`
}

// Config is a collection of apps indexed by name.
type Config map[string]AppConfig
