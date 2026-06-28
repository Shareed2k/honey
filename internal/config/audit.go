package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Audit configures the append-only JSONL audit event log. When Enabled is
// false (the default), no audit file is written and the sink is a no-op.
type Audit struct {
	Enabled bool   `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`
}

// EffectivePath returns the resolved audit log path, expanding a leading "~"
// to the user home directory. Falls back to ~/.honey/audit.jsonl when Path
// is unset.
func (a Audit) EffectivePath() string {
	p := strings.TrimSpace(a.Path)
	if p == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(".honey", "audit.jsonl")
		}
		return filepath.Join(home, ".honey", "audit.jsonl")
	}
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return p
		}
		return filepath.Join(home, p[2:])
	}
	return p
}
