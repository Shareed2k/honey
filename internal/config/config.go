package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/shareed2k/honey/internal/safepath"
)

// File is the optional honey YAML configuration.
type File struct {
	Version  int      `yaml:"version"`
	Defaults Defaults `yaml:"defaults"`
	Backends Backends `yaml:"backends"`
}

// Defaults apply when CLI flags are unset.
type Defaults struct {
	SSHUser       string `yaml:"ssh_user"`
	CacheTTL      string `yaml:"cache_ttl"` // e.g. "5m", "1h"
	K8sMode       string `yaml:"k8s_mode"`
	K8sDebugImage string `yaml:"k8s_debug_image"`
	CacheDir      string `yaml:"cache_dir"`
	Output        string `yaml:"output"` // e.g. "table", "json", "tui" (default)
	Name          string `yaml:"name"`
	NameRegex     string `yaml:"name_regex"`
}

// Backends lists optional multiple instances per provider type.
// If a slice is nil or omitted, that provider is not defined by the file (use CLI defaults).
// If a slice is non-empty, one backend is created per element.
type Backends struct {
	GCP        []GCPBackend        `yaml:"gcp"`
	AWS        []AWSBackend        `yaml:"aws"`
	Kubernetes []KubernetesBackend `yaml:"kubernetes"`
	Consul     []ConsulBackend     `yaml:"consul"`
	Proxmox    []ProxmoxBackend    `yaml:"proxmox"`
}

// GCPBackend configures one Google Cloud Compute Engine listing.
type GCPBackend struct {
	Name    string `yaml:"name"`
	Project string `yaml:"project"`
	Zone    string `yaml:"zone"`
}

// AWSBackend configures one Amazon EC2 listing.
type AWSBackend struct {
	Name    string `yaml:"name"`
	Profile string `yaml:"profile"`
	Region  string `yaml:"region"`
}

// KubernetesBackend configures one Kubernetes nodes/pods listing.
type KubernetesBackend struct {
	Name       string `yaml:"name"`
	Context    string `yaml:"context"`
	Kubeconfig string `yaml:"kubeconfig"`
	Mode       string `yaml:"mode"`
	DebugImage string `yaml:"debug_image"`
}

// ConsulBackend configures one HashiCorp Consul catalog listing.
type ConsulBackend struct {
	Name       string `yaml:"name"`
	Addr       string `yaml:"addr"`
	Datacenter string `yaml:"datacenter"`
	Token      string `yaml:"token"`
}

// ProxmoxBackend configures one Proxmox VE listing.
type ProxmoxBackend struct {
	Name        string `yaml:"name"`
	URL         string `yaml:"url"`
	User        string `yaml:"user"`
	Password    string `yaml:"password"`
	TokenID     string `yaml:"token_id"`
	TokenSecret string `yaml:"token_secret"`
	Insecure    bool   `yaml:"insecure"`
}

// Load reads and parses a YAML config file.
func Load(path string) (*File, error) {
	if path == "" {
		return nil, errors.New("config path empty")
	}
	zap.L().Debug("loading config file", zap.String("path", path))
	b, err := safepath.ReadFile(path)
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
		len(f.Backends.Consul) > 0 ||
		len(f.Backends.Proxmox) > 0
}

// ResolvePath returns an explicit path from --config or HONEY_CONFIG
// then the first existing default file, or "" if none exist.
func ResolvePath(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Abs(filepath.Clean(strings.TrimSpace(explicit)))
	}
	if v := strings.TrimSpace(os.Getenv("HONEY_CONFIG")); v != "" {
		return filepath.Abs(filepath.Clean(v))
	}
	var candidates []string
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		if p, err := safepath.JoinUnder(base, "honey", "config.yaml"); err == nil {
			candidates = append(candidates, p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")) == "" {
			if p, err := safepath.JoinUnder(home, ".config", "honey", "config.yaml"); err == nil {
				candidates = append(candidates, p)
			}
		}
		if p, err := safepath.JoinUnder(home, ".honey.yaml"); err == nil {
			candidates = append(candidates, p)
		}
	}
	for _, p := range candidates {
		if st, err := safepath.Stat(p); err == nil && !st.IsDir() {
			zap.L().Debug("resolved config path via default candidates", zap.String("path", p))
			return p, nil
		}
	}
	zap.L().Debug("no config file resolved")
	return "", nil
}
