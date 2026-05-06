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
	Version  int      `yaml:"version" json:"version"`
	Defaults Defaults `yaml:"defaults" json:"defaults"`
	Backends Backends `yaml:"backends" json:"backends"`
}

// Defaults apply when CLI flags are unset.
type Defaults struct {
	SSHUser       string `yaml:"ssh_user" json:"ssh_user"`
	CacheTTL      string `yaml:"cache_ttl" json:"cache_ttl"` // e.g. "5m", "1h"
	K8sMode       string `yaml:"k8s_mode" json:"k8s_mode"`
	K8sDebugImage string `yaml:"k8s_debug_image" json:"k8s_debug_image"`
	CacheDir      string `yaml:"cache_dir" json:"cache_dir"`
	Output        string `yaml:"output" json:"output"` // e.g. "table", "json", "tui" (default)
	Name          string `yaml:"name" json:"name"`
	NameRegex     string `yaml:"name_regex" json:"name_regex"`
}

// Backends lists optional multiple instances per provider type.
// If a slice is nil or omitted, that provider is not defined by the file (use CLI defaults).
// If a slice is non-empty, one backend is created per element.
type Backends struct {
	GCP        []GCPBackend        `yaml:"gcp" json:"gcp"`
	AWS        []AWSBackend        `yaml:"aws" json:"aws"`
	Kubernetes []KubernetesBackend `yaml:"kubernetes" json:"kubernetes"`
	Consul     []ConsulBackend     `yaml:"consul" json:"consul"`
	Proxmox    []ProxmoxBackend    `yaml:"proxmox" json:"proxmox"`
}

// GCPBackend configures one Google Cloud Compute Engine listing.
type GCPBackend struct {
	Name    string `yaml:"name" json:"name"`
	Project string `yaml:"project" json:"project"`
	Zone    string `yaml:"zone" json:"zone"`
}

// AWSBackend configures one Amazon EC2 listing.
type AWSBackend struct {
	Name    string `yaml:"name" json:"name"`
	Profile string `yaml:"profile" json:"profile"`
	Region  string `yaml:"region" json:"region"`
}

// KubernetesBackend configures one Kubernetes nodes/pods listing.
type KubernetesBackend struct {
	Name       string `yaml:"name" json:"name"`
	Context    string `yaml:"context" json:"context"`
	Kubeconfig string `yaml:"kubeconfig" json:"kubeconfig"`
	Mode       string `yaml:"mode" json:"mode"`
	DebugImage string `yaml:"debug_image" json:"debug_image"`
}

// ConsulBackend configures one HashiCorp Consul catalog listing.
type ConsulBackend struct {
	Name       string `yaml:"name" json:"name"`
	Addr       string `yaml:"addr" json:"addr"`
	Datacenter string `yaml:"datacenter" json:"datacenter"`
	Token      string `yaml:"token" json:"token"`
}

// ProxmoxBackend configures one Proxmox VE listing.
type ProxmoxBackend struct {
	Name        string `yaml:"name" json:"name"`
	URL         string `yaml:"url" json:"url"`
	User        string `yaml:"user" json:"user"`
	Password    string `yaml:"password" json:"password"`
	TokenID     string `yaml:"token_id" json:"token_id"`
	TokenSecret string `yaml:"token_secret" json:"token_secret"`
	Insecure    bool   `yaml:"insecure" json:"insecure"`
}

// Save serializes the config and writes it to path.
func (f *File) Save(path string) error {
	if path == "" {
		return errors.New("config path empty")
	}
	b, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	if err := safepath.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
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

// ParseYAML parses a honey config document from memory (used by web API PUT validation).
func ParseYAML(b []byte) (*File, error) {
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
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
