package config

// BackendRow describes one YAML backends.* entry (for CLI / MCP listing).
type BackendRow struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Hint string `json:"hint,omitempty"`
}

// HoneyBackend configures a remote honey server backend.
type HoneyBackend struct {
	Name     string `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	URL      string `yaml:"url" json:"url" honey:"label=Honey Server URL" validate:"required,url" mod:"trim"`
	Token    string `yaml:"token,omitempty" json:"token,omitempty" honey:"label=Auth Token;secret" mod:"trim"`
	Insecure bool   `yaml:"insecure" json:"insecure" honey:"label=Insecure TLS;default=false"`
}
