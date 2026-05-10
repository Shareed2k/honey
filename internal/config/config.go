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
	SSHUser       string `yaml:"ssh_user" json:"ssh_user" honey:"label=SSH user"`
	CacheTTL      string `yaml:"cache_ttl" json:"cache_ttl" honey:"label=Cache TTL"` // e.g. "5m", "1h"
	K8sMode       string `yaml:"k8s_mode" json:"k8s_mode" honey:"label=Kubernetes mode;enum=nodes|pods;enum_as_warning"`
	K8sDebugImage string `yaml:"k8s_debug_image" json:"k8s_debug_image" honey:"label=Kubernetes debug image"`
	CacheDir      string `yaml:"cache_dir" json:"cache_dir" honey:"label=Cache directory"`
	RecordDir     string `yaml:"record_dir" json:"record_dir" honey:"label=Session recordings directory"`
	Output        string `yaml:"output" json:"output" honey:"label=Output;enum=table|json|tui;enum_as_warning"` // e.g. "table", "json", "tui" (default)
	Name          string `yaml:"name" json:"name" honey:"label=Name filter"`
	NameRegex     string `yaml:"name_regex" json:"name_regex" honey:"label=Name regex"`
}

// Backends lists optional multiple instances per provider type.
// If a slice is nil or omitted, that provider is not defined by the file (use CLI defaults).
// If a slice is non-empty, one backend is created per element.
type Backends struct {
	GCP        []GCPBackend        `yaml:"gcp" json:"gcp" honey:"label=Google Cloud;order=10"`
	AWS        []AWSBackend        `yaml:"aws" json:"aws" honey:"label=AWS;order=20"`
	Kubernetes []KubernetesBackend `yaml:"kubernetes" json:"kubernetes" honey:"label=Kubernetes;order=30"`
	Consul     []ConsulBackend     `yaml:"consul" json:"consul" honey:"label=Consul;order=40"`
	Proxmox    []ProxmoxBackend    `yaml:"proxmox" json:"proxmox" honey:"label=Proxmox;order=50"`
}

// GCPBackend configures one Google Cloud Compute Engine listing.
type GCPBackend struct {
	Name    string `yaml:"name" json:"name" honey:"label=Name"`
	Project string `yaml:"project" json:"project" honey:"label=Project"`
	Zone    string `yaml:"zone" json:"zone" honey:"label=Zone"`
}

// AWSBackend configures one Amazon EC2 listing.
type AWSBackend struct {
	Name    string `yaml:"name" json:"name" honey:"label=Name"`
	Profile string `yaml:"profile" json:"profile" honey:"label=Profile"`
	Region  string `yaml:"region" json:"region" honey:"label=Region"`
}

// KubernetesBackend configures one Kubernetes nodes/pods listing.
type KubernetesBackend struct {
	Name       string `yaml:"name" json:"name" honey:"label=Name"`
	Context    string `yaml:"context" json:"context" honey:"label=Context"`
	Kubeconfig string `yaml:"kubeconfig" json:"kubeconfig" honey:"label=Kubeconfig path"`
	Mode       string `yaml:"mode" json:"mode" honey:"label=Mode;enum=nodes|pods;enum_as_warning;default=nodes"`
	DebugImage string `yaml:"debug_image" json:"debug_image" honey:"label=Debug image"`
}

// ConsulBackend configures one HashiCorp Consul catalog listing.
type ConsulBackend struct {
	Name       string `yaml:"name" json:"name" honey:"label=Name"`
	Addr       string `yaml:"addr" json:"addr" honey:"label=Address"`
	Datacenter string `yaml:"datacenter" json:"datacenter" honey:"label=Datacenter"`
	Token      string `yaml:"token" json:"token" honey:"label=Token;secret"`
}

// ProxmoxBackend configures one Proxmox VE listing.
type ProxmoxBackend struct {
	Name        string `yaml:"name" json:"name" honey:"label=Name"`
	URL         string `yaml:"url" json:"url" honey:"label=URL"`
	User        string `yaml:"user" json:"user" honey:"label=User"`
	Password    string `yaml:"password" json:"password" honey:"label=Password;secret"`
	TokenID     string `yaml:"token_id" json:"token_id" honey:"label=Token ID"`
	TokenSecret string `yaml:"token_secret" json:"token_secret" honey:"label=Token secret;secret"`
	Insecure    bool   `yaml:"insecure" json:"insecure" honey:"label=Insecure TLS;default=false"`
	// ExecMode: ssh (default) = guest SSH for commands/SFTP/tunnels; pve = QEMU commands via guest agent API, LXC commands/SFTP over guest SSH (PVE has no LXC REST exec; web UI LXC console uses termproxy when token_id is set);
	// hybrid = QEMU via guest agent + SSH for files; LXC uses guest SSH for commands and files.
	ExecMode string `yaml:"exec_mode" json:"exec_mode" honey:"label=Exec mode;enum=ssh|pve|hybrid;enum_as_warning"`
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

// DefaultRecordDir returns the directory used for session recordings when --record-dir
// is not set: <directory of config.yaml>/records (e.g. ~/.config/honey/records). If
// configPath is empty, returns the conventional honey config directory (.../honey/records)
// matching default config.yaml search paths.
func DefaultRecordDir(configPath string) string {
	configPath = strings.TrimSpace(configPath)
	if configPath != "" {
		if abs, err := filepath.Abs(filepath.Clean(configPath)); err == nil {
			return filepath.Join(filepath.Dir(abs), "records")
		}
	}
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		if p, err := safepath.JoinUnder(base, "honey", "records"); err == nil {
			return p
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")) == "" {
			if p, err := safepath.JoinUnder(home, ".config", "honey", "records"); err == nil {
				return p
			}
		}
	}
	return ""
}

// ResolveRecordDir returns the session recordings directory (CLI TUI, web server, cue-exec).
// Precedence when recordDirFlagChanged is true: non-empty global --record-dir value,
// otherwise DefaultRecordDir(configPath) (explicit empty flag keeps the default path).
// When recordDirFlagChanged is false: defaults.record_dir from cfg if set,
// otherwise DefaultRecordDir(configPath).
func ResolveRecordDir(cfg *File, configPath string, recordDirFlag string, recordDirFlagChanged bool) string {
	v := strings.TrimSpace(recordDirFlag)
	if recordDirFlagChanged {
		if v != "" {
			return v
		}
		return strings.TrimSpace(DefaultRecordDir(configPath))
	}
	if cfg != nil {
		if s := strings.TrimSpace(cfg.Defaults.RecordDir); s != "" {
			return s
		}
	}
	return strings.TrimSpace(DefaultRecordDir(configPath))
}
