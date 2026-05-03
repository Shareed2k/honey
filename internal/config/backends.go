package config

// BackendRow describes one YAML backends.* entry (for CLI / MCP listing).
type BackendRow struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Hint string `json:"hint,omitempty"`
}
