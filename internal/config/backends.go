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
	URL      string `yaml:"url" json:"url" honey:"label=Honey Server URL" validate:"required" mod:"trim"`
	Token    string `yaml:"token,omitempty" json:"token,omitempty" honey:"label=Auth Token;secret" mod:"trim"`
	Insecure bool   `yaml:"insecure" json:"insecure" honey:"label=Insecure TLS;default=false"`
	// MTLS marks this backend as managed by the device mTLS client (the mobile app
	// fetches it over a client cert). The in-process honeyprovider skips it to
	// avoid a double-fetch; the app owns it. See examples/mtls/apisix.
	MTLS bool `yaml:"mtls,omitempty" json:"mtls,omitempty" honey:"label=Client-cert (mTLS) managed;default=false"`
	// ServerCA optionally pins the gateway server certificate (PEM) the mTLS
	// client trusts. Empty falls back to the enrolled device CA.
	ServerCA string `yaml:"server_ca,omitempty" json:"server_ca,omitempty" honey:"label=Gateway server CA (PEM);secret" mod:"trim"`
}
