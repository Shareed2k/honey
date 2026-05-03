package config

import "strings"

// BackendRow describes one YAML backends.* entry (for CLI / MCP listing).
type BackendRow struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Hint string `json:"hint,omitempty"`
}

// ListBackendRows returns a flat list of configured backends, in stable order:
// gcp, aws, kubernetes, consul. Nil when the file defines no backend entries.
func (f *File) ListBackendRows() []BackendRow {
	if f == nil || !f.HasAnyBackend() {
		return nil
	}
	rows := make([]BackendRow, 0, len(f.Backends.GCP)+len(f.Backends.AWS)+len(f.Backends.Kubernetes)+len(f.Backends.Consul))
	for _, e := range f.Backends.GCP {
		rows = append(rows, BackendRow{Kind: "gcp", Name: e.Name, Hint: strings.TrimSpace(e.Project)})
	}
	for _, e := range f.Backends.AWS {
		rows = append(rows, BackendRow{Kind: "aws", Name: e.Name, Hint: strings.TrimSpace(e.Profile + " " + e.Region)})
	}
	for _, e := range f.Backends.Kubernetes {
		rows = append(rows, BackendRow{Kind: "kubernetes", Name: e.Name, Hint: strings.TrimSpace(e.Context)})
	}
	for _, e := range f.Backends.Consul {
		rows = append(rows, BackendRow{Kind: "consul", Name: e.Name, Hint: strings.TrimSpace(e.Addr)})
	}
	return rows
}
