package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// File is the optional hostctl YAML configuration.
type File struct {
	Version  int      `yaml:"version"`
	Defaults Defaults `yaml:"defaults"`
	Backends Backends `yaml:"backends"`
}

// Defaults apply when CLI flags are unset.
type Defaults struct {
	SSHUser   string `yaml:"ssh_user"`
	CacheTTL  string `yaml:"cache_ttl"` // e.g. "5m", "1h"
	K8sMode   string `yaml:"k8s_mode"`
	CacheDir  string `yaml:"cache_dir"`
	Name      string `yaml:"name"`
	NameRegex string `yaml:"name_regex"`
}

// Backends lists optional multiple instances per provider type.
// If a slice is nil or omitted, that provider is not defined by the file (use CLI defaults).
// If a slice is non-empty, one backend is created per element.
type Backends struct {
	GCP          []GCPBackend          `yaml:"gcp"`
	AWS          []AWSBackend          `yaml:"aws"`
	Kubernetes   []KubernetesBackend   `yaml:"kubernetes"`
	Consul       []ConsulBackend       `yaml:"consul"`
}

type GCPBackend struct {
	Name    string `yaml:"name"`
	Project string `yaml:"project"`
	Zone    string `yaml:"zone"`
}

type AWSBackend struct {
	Name    string `yaml:"name"`
	Profile string `yaml:"profile"`
	Region  string `yaml:"region"`
}

type KubernetesBackend struct {
	Name       string `yaml:"name"`
	Context    string `yaml:"context"`
	Kubeconfig string `yaml:"kubeconfig"`
	Mode       string `yaml:"mode"`
}

type ConsulBackend struct {
	Name       string `yaml:"name"`
	Addr       string `yaml:"addr"`
	Datacenter string `yaml:"datacenter"`
	Token      string `yaml:"token"`
}

// Load reads and parses a YAML config file.
func Load(path string) (*File, error) {
	if path == "" {
		return nil, errors.New("config path empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &f, nil
}

// DefaultsCacheTTL parses Defaults.CacheTTL or returns empty and ok=false.
func (d Defaults) DefaultsCacheTTL() (time.Duration, bool, error) {
	if strings.TrimSpace(d.CacheTTL) == "" {
		return 0, false, nil
	}
	t, err := time.ParseDuration(strings.TrimSpace(d.CacheTTL))
	if err != nil {
		return 0, false, err
	}
	return t, true, nil
}

// HasAnyBackend returns true if the file defines at least one backend entry.
func (f *File) HasAnyBackend() bool {
	if f == nil {
		return false
	}
	return len(f.Backends.GCP) > 0 ||
		len(f.Backends.AWS) > 0 ||
		len(f.Backends.Kubernetes) > 0 ||
		len(f.Backends.Consul) > 0
}

// ResolvePath returns an explicit path from --config or HOSTCTL_CONFIG, or the first
// existing default file, or "" if none exist.
func ResolvePath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	if v := strings.TrimSpace(os.Getenv("HOSTCTL_CONFIG")); v != "" {
		return v, nil
	}
	candidates := []string{}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		candidates = append(candidates, filepath.Join(base, "hostctl", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		if base := os.Getenv("XDG_CONFIG_HOME"); base == "" {
			candidates = append(candidates, filepath.Join(home, ".config", "hostctl", "config.yaml"))
		}
		candidates = append(candidates, filepath.Join(home, ".hostctl.yaml"))
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", nil
}
