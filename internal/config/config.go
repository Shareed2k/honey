package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/safepath"
)

// File is the optional honey YAML configuration.
type File struct {
	Version  int            `yaml:"version" json:"version"`
	Defaults Defaults       `yaml:"defaults" json:"defaults"`
	Backends Backends       `yaml:"backends" json:"backends"`
	Transfer TransferConfig `yaml:"transfer" json:"transfer"`
	Plugins  Plugins        `yaml:"plugins,omitempty" json:"plugins,omitempty"`
	Apps     apps.Config    `yaml:"apps,omitempty" json:"apps,omitempty"`
}

// DockerDiscover configures auto-discovery of containers on cloud VMs.
type DockerDiscover struct {
	Enabled  bool   `yaml:"enabled" json:"enabled" honey:"label=Enable auto-discover"`
	RunAs    string `yaml:"run_as,omitempty" json:"run_as,omitempty" honey:"label=Remote user for docker.sock via sudo (e.g. root)"`
	Socket   string `yaml:"socket,omitempty" json:"socket,omitempty" honey:"label=Remote Docker socket"`
	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" honey:"label=Remote OS;enum=linux|windows"`
}

// Logs holds per-command defaults for honey logs.
type Logs struct {
	Anomaly                bool    `yaml:"anomaly"                     json:"anomaly"                     honey:"label=Enable anomaly detection"`
	AnomalyModel           string  `yaml:"anomaly_model,omitempty"     json:"anomaly_model,omitempty"     honey:"label=Path to ONNX model file"`
	AnomalyThresh          float64 `yaml:"anomaly_threshold,omitempty" json:"anomaly_threshold,omitempty" honey:"label=Anomaly score threshold (0–1)"`
	AnomalyWindow          int     `yaml:"anomaly_window,omitempty"    json:"anomaly_window,omitempty"    honey:"label=Anomaly sliding window size"`
	AnomalyOnly            bool    `yaml:"anomaly_only"                json:"anomaly_only"                honey:"label=Only output anomalous lines"`
	AnomalyStrict          bool    `yaml:"anomaly_strict"              json:"anomaly_strict"              honey:"label=Fail if anomaly detector cannot init"`
	AnomalyTokPath         string  `yaml:"anomaly_tokenizer,omitempty"  json:"anomaly_tokenizer,omitempty"  honey:"label=Path to vocab.txt tokenizer file"`
	AnomalyEndpoint        string  `yaml:"anomaly_endpoint,omitempty"      json:"anomaly_endpoint,omitempty"      honey:"label=OpenAI-compatible API URL for LLM anomaly detection (Ollama/LM Studio)"`
	AnomalyLLMModel        string  `yaml:"anomaly_llm_model,omitempty"     json:"anomaly_llm_model,omitempty"     honey:"label=Model name for LLM anomaly endpoint"`
	AnomalyContextLines    int     `yaml:"anomaly_context_lines,omitempty"    json:"anomaly_context_lines,omitempty"    honey:"label=Number of recent lines sent as context to the LLM anomaly detector"`
	AnomalyFilterThreshold float64 `yaml:"anomaly_filter_threshold,omitempty" json:"anomaly_filter_threshold,omitempty" honey:"label=Skip LLM when fast detector score is below this value (CoLA two-tier; 0=disabled)"`
	AnomalyFreqWindow      int     `yaml:"anomaly_freq_window,omitempty"      json:"anomaly_freq_window,omitempty"      honey:"label=Short window size for rate-ratio burst detection (0=disabled, default 100)"`
	AnomalyFreqRatio       float64 `yaml:"anomaly_freq_ratio,omitempty"       json:"anomaly_freq_ratio,omitempty"       honey:"label=Short/long rate ratio that triggers a frequency-spike anomaly (default 5.0)"`
	AnomalyFeedbackFile    string  `yaml:"anomaly_feedback_file,omitempty"   json:"anomaly_feedback_file,omitempty"    honey:"label=Append scored log lines as JSONL to this file for review and threshold calibration"`
	AlertEnabled           bool    `yaml:"alert_enabled"              json:"alert_enabled"              honey:"label=Alert on anomalies"`
	AlertSuppressDuration  string  `yaml:"alert_suppress_duration,omitempty" json:"alert_suppress_duration,omitempty" honey:"label=Alert suppression window (e.g. 5m)"`
}

// Defaults apply when CLI flags are unset.
type Defaults struct {
	SSHUser         string         `yaml:"ssh_user" json:"ssh_user" honey:"label=SSH user"`
	CacheTTL        string         `yaml:"cache_ttl" json:"cache_ttl" honey:"label=Cache TTL"` // e.g. "5m", "1h"
	K8sMode         string         `yaml:"k8s_mode" json:"k8s_mode" honey:"label=Kubernetes mode;enum=nodes|pods;enum_as_warning"`
	K8sDebugImage   string         `yaml:"k8s_debug_image" json:"k8s_debug_image" honey:"label=Kubernetes debug image"`
	CacheDir        string         `yaml:"cache_dir" json:"cache_dir" honey:"label=Cache directory"`
	RecordDir       string         `yaml:"record_dir" json:"record_dir" honey:"label=Session recordings directory"`
	RecordRetention string         `yaml:"record_retention" json:"record_retention" honey:"label=Auto-delete recordings older than this (e.g. 720h, 30d); empty disables"`
	Output          string         `yaml:"output" json:"output" honey:"label=Output;enum=table|json|tui;enum_as_warning"` // e.g. "table", "json", "tui" (default)
	Name            string         `yaml:"name" json:"name" honey:"label=Name filter"`
	NameRegex       string         `yaml:"name_regex" json:"name_regex" honey:"label=Name regex"`
	AISystemPrompt  string         `yaml:"ai_system_prompt" json:"ai_system_prompt" honey:"label=Default system prompt for CUE recipe ai step"`
	DockerDiscover  DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover Defaults"`
	Logs            Logs           `yaml:"logs,omitempty" json:"logs,omitempty" honey:"label=Logs command defaults"`

	// secretsprovider unwraps the stack AES data key (see internal/cuetry/secrets/doc.go).
	// Examples: gcpkms://projects/…/cryptoKeys/…, awskms://, vault-transit://mount/key,
	// k8s://namespace/secret, keyring://service/user, age://, age-file://path.
	SecretsProvider string `yaml:"secretsprovider,omitempty" json:"secretsprovider,omitempty" honey:"label=Stack secrets provider URL (e.g. gcpkms://…);reserved"`
	// encryptedkey is provider-specific ciphertext or field name (see secrets package doc).
	EncryptedKey string `yaml:"encryptedkey,omitempty" json:"encryptedkey,omitempty" honey:"label=Stack encrypted data key blob;secret;reserved"`
}

// Backends lists optional multiple instances per provider type.
// If a slice is nil or omitted, that provider is not defined by the file (use CLI defaults).
// If a slice is non-empty, one backend is created per element.
type Backends struct {
	GCP        []GCPBackend        `yaml:"gcp" json:"gcp" honey:"label=Google Cloud;order=10" validate:"dive"`
	AWS        []AWSBackend        `yaml:"aws" json:"aws" honey:"label=AWS;order=20" validate:"dive"`
	Kubernetes []KubernetesBackend `yaml:"kubernetes" json:"kubernetes" honey:"label=Kubernetes;order=30" validate:"dive"`
	Consul     []ConsulBackend     `yaml:"consul" json:"consul" honey:"label=Consul;order=40" validate:"dive"`
	Proxmox    []ProxmoxBackend    `yaml:"proxmox" json:"proxmox" honey:"label=Proxmox;order=50" validate:"dive"`
	TrueNAS    []TrueNASBackend    `yaml:"truenas" json:"truenas" honey:"label=TrueNAS;order=55" validate:"dive"`
	Local      []LocalBackend      `yaml:"local" json:"local" honey:"label=Local;order=60" validate:"dive"`
	Docker     []DockerBackend     `yaml:"docker" json:"docker" honey:"label=Docker;order=35" validate:"dive"`
}

// LocalBackend configures manually defined host lists.
type LocalBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	Hosts          []LocalHost    `yaml:"hosts" json:"hosts" honey:"label=Hosts" validate:"dive"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// LocalHost represents a manually defined static server.
type LocalHost struct {
	Name      string            `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	PrimaryIP string            `yaml:"primary_ip" json:"primary_ip" honey:"label=Primary IP" validate:"required,ip"`
	ExtraIPs  []string          `yaml:"extra_ips,omitempty" json:"extra_ips,omitempty" honey:"label=Extra IPs" validate:"dive,ip"`
	Zone      string            `yaml:"zone,omitempty" json:"zone,omitempty" honey:"label=Zone"`
	Region    string            `yaml:"region,omitempty" json:"region,omitempty" honey:"label=Region"`
	Meta      map[string]string `yaml:"meta,omitempty" json:"meta,omitempty" honey:"label=Metadata"`
}

// GCPBackend configures one Google Cloud Compute Engine listing.
type GCPBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	Project        string         `yaml:"project" json:"project" honey:"label=Project" validate:"required"`
	Zone           string         `yaml:"zone" json:"zone" honey:"label=Zone"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// AWSBackend configures one Amazon EC2 listing.
type AWSBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	Profile        string         `yaml:"profile" json:"profile" honey:"label=Profile" validate:"required"`
	Region         string         `yaml:"region" json:"region" honey:"label=Region"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// KubernetesBackend configures one Kubernetes nodes/pods listing.
type KubernetesBackend struct {
	Name       string `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	Context    string `yaml:"context" json:"context" honey:"label=Context"`
	Kubeconfig string `yaml:"kubeconfig" json:"kubeconfig" honey:"label=Kubeconfig path"`
	Mode       string `yaml:"mode" json:"mode" honey:"label=Mode;enum=nodes|pods;enum_as_warning;default=nodes"`
	DebugImage string `yaml:"debug_image" json:"debug_image" honey:"label=Debug image"`
}

// ConsulBackend configures one HashiCorp Consul catalog listing.
type ConsulBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	Addr           string         `yaml:"addr" json:"addr" honey:"label=Address" validate:"required,url"`
	Datacenter     string         `yaml:"datacenter" json:"datacenter" honey:"label=Datacenter"`
	Token          string         `yaml:"token" json:"token" honey:"label=Token;secret"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// ProxmoxBackend configures one Proxmox VE listing.
type ProxmoxBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	URL            string         `yaml:"url" json:"url" honey:"label=URL" validate:"required,url"`
	User           string         `yaml:"user" json:"user" honey:"label=User" validate:"required_without=TokenID"`
	Password       string         `yaml:"password" json:"password" honey:"label=Password;secret" validate:"required_without=TokenSecret"`
	TokenID        string         `yaml:"token_id" json:"token_id" honey:"label=Token ID" validate:"required_without=User"`
	TokenSecret    string         `yaml:"token_secret" json:"token_secret" honey:"label=Token secret;secret" validate:"required_without=Password"`
	Insecure       bool           `yaml:"insecure" json:"insecure" honey:"label=Insecure TLS;default=false"`
	ExecMode       string         `yaml:"exec_mode" json:"exec_mode" honey:"label=Exec mode;enum=ssh|pve|hybrid;enum_as_warning"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// TrueNASBackend configures one TrueNAS SCALE controller (WebSocket API 25.04+).
type TrueNASBackend struct {
	Name             string `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	URL              string `yaml:"url" json:"url" honey:"label=URL" validate:"required,url"`
	Username         string `yaml:"username,omitempty" json:"username,omitempty" honey:"label=API key username (default root)"`
	APIKey           string `yaml:"api_key" json:"api_key" honey:"label=API key;secret" validate:"required"`
	Insecure         bool   `yaml:"insecure" json:"insecure" honey:"label=Insecure TLS;default=false"`
	IncludeAppliance *bool  `yaml:"include_appliance,omitempty" json:"include_appliance,omitempty" honey:"label=List appliance;default=true"`
	IncludeVMs       *bool  `yaml:"include_vms,omitempty" json:"include_vms,omitempty" honey:"label=List KVM VMs;default=true"`
	IncludeVirt      *bool  `yaml:"include_virt,omitempty" json:"include_virt,omitempty" honey:"label=List virt instances;default=true"`
	SSHUser          string `yaml:"ssh_user,omitempty" json:"ssh_user,omitempty" honey:"label=SSH user for appliance"`
}

// DockerViaSSH configures an explicit SSH hop for Honey's SSH stack (not Moby ssh://).
type DockerViaSSH struct {
	Host         string `yaml:"host" json:"host" honey:"label=SSH host"`
	Port         int    `yaml:"port,omitempty" json:"port,omitempty" honey:"label=SSH port (0 = ssh_config default)"`
	User         string `yaml:"user,omitempty" json:"user,omitempty" honey:"label=SSH user"`
	IdentityFile string `yaml:"identity_file,omitempty" json:"identity_file,omitempty" honey:"label=SSH identity file"`
}

// DockerBackend configures one Docker Engine API endpoint (local socket, tcp, ssh://, or Honey SSH).
type DockerBackend struct {
	Name          string       `yaml:"name" json:"name" honey:"label=Name" validate:"required"`
	Host          string       `yaml:"host" json:"host" honey:"label=Host (unix://, tcp://, ssh://; empty = DOCKER_HOST / local socket)"`
	ViaLocal      string       `yaml:"via_local,omitempty" json:"via_local,omitempty" honey:"label=Local backend name (SSH hop via backends.local)"`
	ViaSSH        DockerViaSSH `yaml:"via_ssh,omitempty" json:"via_ssh,omitempty" honey:"label=SSH hop (overrides via_local when host set)"`
	Socket        string       `yaml:"socket,omitempty" json:"socket,omitempty" honey:"label=Remote Engine socket (default /var/run/docker.sock on linux)"`
	Platform      string       `yaml:"platform,omitempty" json:"platform,omitempty" honey:"label=Remote OS;enum=linux|windows;enum_as_warning;default=linux"`
	RunAs         string       `yaml:"run_as,omitempty" json:"run_as,omitempty" honey:"label=Remote user for docker.sock via sudo (honey-ssh only)"`
	Mode          string       `yaml:"mode" json:"mode" honey:"label=Mode;enum=containers|swarm|both;enum_as_warning;default=containers"`
	AllContainers bool         `yaml:"all_containers" json:"all_containers" honey:"label=Include stopped containers;default=false"`
	TLSVerify     bool         `yaml:"tls_verify" json:"tls_verify" honey:"label=Verify TLS (tcp hosts);default=true"`
	CACert        string       `yaml:"ca_cert" json:"ca_cert" honey:"label=CA certificate path"`
	Cert          string       `yaml:"cert" json:"cert" honey:"label=Client certificate path"`
	Key           string       `yaml:"key" json:"key" honey:"label=Client key path;secret"`
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

// Load reads and parses a YAML config file using viper.
// The returned *File is populated from the YAML file, with any HONEY_* environment
// variables able to override individual fields (e.g. HONEY_DEFAULTS_SSH_USER).
func Load(path string) (*File, error) {
	if path == "" {
		return nil, errors.New("config path empty")
	}
	zap.L().Debug("loading config file", zap.String("path", path))

	v := viper.NewWithOptions(viper.KeyDelimiter("."))
	v.SetConfigFile(path)
	v.SetEnvPrefix("HONEY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	var f File
	if err := v.Unmarshal(&f, func(dc *mapstructure.DecoderConfig) {
		dc.TagName = "yaml"
		dc.WeaklyTypedInput = true
	}); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}

	if f.Apps != nil {
		for name, app := range f.Apps {
			app.Name = name
			f.Apps[name] = app
		}
	}

	return &f, nil
}

// ParseYAML parses a honey config document from memory (used by web API PUT validation).
func ParseYAML(b []byte) (*File, error) {
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if f.Apps != nil {
		for name, app := range f.Apps {
			app.Name = name
			f.Apps[name] = app
		}
	}
	if err := f.Validate(); err != nil {
		return nil, err
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

// ParseRetentionDuration parses a retention duration (supports Go durations and day suffix, e.g. 30d).
func ParseRetentionDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}
	if strings.HasSuffix(s, "d") {
		n := strings.TrimSpace(strings.TrimSuffix(s, "d"))
		if n == "" {
			return 0, fmt.Errorf("invalid retention duration %q", s)
		}
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid retention duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// DefaultsRecordRetention parses Defaults.RecordRetention or returns empty and ok=false.
func (d Defaults) DefaultsRecordRetention() (time.Duration, bool, error) {
	if strings.TrimSpace(d.RecordRetention) == "" {
		return 0, false, nil
	}
	t, err := ParseRetentionDuration(d.RecordRetention)
	if err != nil {
		return 0, false, err
	}
	if t <= 0 {
		return 0, false, nil
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
		len(f.Backends.Proxmox) > 0 ||
		len(f.Backends.Local) > 0 ||
		len(f.Backends.Docker) > 0
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

// TransferConfig controls the agent-transfer code path. Zero values mean "use
// defaults" — call WithDefaults() to materialize.
type TransferConfig struct {
	PresignedMaxSize        string `yaml:"presigned_max_size,omitempty" json:"presigned_max_size,omitempty"`
	MultipartThreshold      string `yaml:"multipart_threshold,omitempty" json:"multipart_threshold,omitempty"`
	PresignedURLTTL         string `yaml:"presigned_url_ttl,omitempty" json:"presigned_url_ttl,omitempty"`
	PresignedRetryWithAgent *bool  `yaml:"presigned_retry_with_agent,omitempty" json:"presigned_retry_with_agent,omitempty"`
	ForceAgentPath          bool   `yaml:"force_agent_path,omitempty" json:"force_agent_path,omitempty"`
}

// TransferConfigEffective is the post-defaults form used by callers.
type TransferConfigEffective struct {
	PresignedMaxSizeBytes   int64
	MultipartThresholdBytes int64
	PresignedURLTTL         time.Duration
	PresignedRetryWithAgent bool
	ForceAgentPath          bool
}

// WithDefaults returns an effective config, parsing strings to bytes / durations
// and substituting defaults for unset fields.
func (c TransferConfig) WithDefaults() TransferConfigEffective {
	const (
		defaultMaxSize            int64 = 5 << 30
		defaultMultipartThreshold int64 = 64 << 20
		defaultURLTTL                   = time.Hour
		minURLTTL                       = 5 * time.Minute
		maxURLTTL                       = 24 * time.Hour
	)

	out := TransferConfigEffective{
		PresignedMaxSizeBytes:   defaultMaxSize,
		MultipartThresholdBytes: defaultMultipartThreshold,
		PresignedURLTTL:         defaultURLTTL,
		PresignedRetryWithAgent: true,
		ForceAgentPath:          c.ForceAgentPath,
	}
	if c.PresignedMaxSize != "" {
		if n, err := ParseBytes(c.PresignedMaxSize); err == nil && n > 0 {
			out.PresignedMaxSizeBytes = n
		}
	}
	if c.MultipartThreshold != "" {
		if n, err := ParseBytes(c.MultipartThreshold); err == nil && n > 0 {
			out.MultipartThresholdBytes = n
		}
	}
	if c.PresignedURLTTL != "" {
		if d, err := time.ParseDuration(c.PresignedURLTTL); err == nil && d > 0 {
			if d < minURLTTL {
				d = minURLTTL
			}
			if d > maxURLTTL {
				d = maxURLTTL
			}
			out.PresignedURLTTL = d
		}
	}
	if c.PresignedRetryWithAgent != nil {
		out.PresignedRetryWithAgent = *c.PresignedRetryWithAgent
	}
	return out
}

// ResolveStateDir returns a safe path for writing runtime state files.
// It prefers XDG_STATE_HOME if set, otherwise ~/.local/state/honey,
// or falls back to the user's home directory.
func ResolveStateDir() (string, error) {
	if base := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); base != "" {
		return safepath.JoinUnder(base, "honey")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return safepath.JoinUnder(home, ".local", "state", "honey")
}
