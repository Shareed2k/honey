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
	// Mesh routes this backend's URL through the local libp2p mesh client
	// (internal/meshnet) instead of the normal network path — for reaching a
	// remote honey instance behind NAT/CGNAT with no port-forward. Requires
	// Config.Mesh.Enabled on this process, with at least one relay
	// configured in Config.Mesh.RelayAddrs.
	Mesh bool `yaml:"mesh,omitempty" json:"mesh,omitempty" honey:"label=Route via mesh;default=false"`
	// MeshAddr is the libp2p multiaddr to dial when Mesh is true — the actual
	// network target, e.g. a direct peer address or a full Circuit Relay v2
	// path (relay + target peer ID). URL is still used for building this
	// backend's request paths (e.g. "/api/v1/search") via plain string
	// concatenation — its host component is never actually resolved for a
	// mesh-routed backend, since the real dial is intercepted and redirected
	// to MeshAddr instead. Required when Mesh is true.
	MeshAddr string `yaml:"mesh_addr,omitempty" json:"mesh_addr,omitempty" honey:"label=Mesh dial address" validate:"required_if=Mesh true" mod:"trim"`
}
