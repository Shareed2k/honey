package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/creasty/defaults"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
)

// File is the optional honey YAML configuration.
type File struct {
	Version       int                `yaml:"version" json:"version"`
	Inventory     Inventory          `yaml:"inventory,omitempty" json:"inventory,omitempty"`
	Defaults      Defaults           `yaml:"defaults" json:"defaults"`
	Backends      Backends           `yaml:"backends" json:"backends"`
	Transfer      TransferConfig     `yaml:"transfer" json:"transfer"`
	Plugins       Plugins            `yaml:"plugins,omitempty" json:"plugins,omitempty"`
	Apps          apps.Config        `yaml:"apps,omitempty" json:"apps,omitempty"`
	AlertMappings []AlertMapping     `yaml:"alert_mappings,omitempty" json:"alert_mappings,omitempty"`
	AlertWebhook  AlertWebhookConfig `yaml:"alert_webhook,omitempty" json:"alert_webhook,omitempty"`
}

// AlertNotifySlack configures Slack notifications for alert findings.
type AlertNotifySlack struct {
	WebhookURL string `yaml:"webhook_url" json:"webhook_url"`
	ChannelID  string `yaml:"channel_id,omitempty" json:"channel_id,omitempty"`
}

// AlertNotifyHTTP configures HTTP POST notifications for alert findings.
type AlertNotifyHTTP struct {
	URL     string            `yaml:"url" json:"url"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

// AlertNotifyTelegram configures Telegram notifications for alert findings.
type AlertNotifyTelegram struct {
	BotToken string   `yaml:"bot_token,omitempty" json:"bot_token,omitempty"`
	ChatIDs  []string `yaml:"chat_ids,omitempty" json:"chat_ids,omitempty"`
}

// AlertNotify holds optional notification config for alert investigation findings.
type AlertNotify struct {
	Subject  string               `yaml:"subject,omitempty" json:"subject,omitempty"`
	Slack    *AlertNotifySlack    `yaml:"slack,omitempty" json:"slack,omitempty"`
	HTTP     *AlertNotifyHTTP     `yaml:"http,omitempty" json:"http,omitempty"`
	Telegram *AlertNotifyTelegram `yaml:"telegram,omitempty" json:"telegram,omitempty"`
}

// AlertMapping maps Prometheus alert labels to a honey host query and optional investigation config.
type AlertMapping struct {
	MatchLabels map[string]string `yaml:"match_labels" json:"match_labels"`
	// HostQuery is a Go template evaluated against alert labels to produce a honey search query.
	// Example: "{{.cluster}}" resolves the cluster label to a substring host search.
	HostQuery string       `yaml:"host_query" json:"host_query"`
	Recipe    string       `yaml:"recipe,omitempty" json:"recipe,omitempty"`
	Command   string       `yaml:"command,omitempty" json:"command,omitempty"`
	Notify    *AlertNotify `yaml:"notify,omitempty" json:"notify,omitempty"`
}

// AlertWebhookConfig configures the Alertmanager webhook receiver server.
type AlertWebhookConfig struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`
	Port            int    `yaml:"port" json:"port"`
	Token           string `yaml:"token,omitempty" json:"token,omitempty"`
	AutoInvestigate bool   `yaml:"auto_investigate" json:"auto_investigate"`
	DedupWindow     string `yaml:"dedup_window,omitempty" json:"dedup_window,omitempty"`
	DedupCapacity   int    `yaml:"dedup_capacity,omitempty" json:"dedup_capacity,omitempty"`
}

// DockerDiscover configures auto-discovery of containers on cloud VMs.
type DockerDiscover struct {
	Enabled  bool   `yaml:"enabled" json:"enabled" honey:"label=Enable auto-discover"`
	RunAs    string `yaml:"run_as,omitempty" json:"run_as,omitempty" honey:"label=Remote user for docker.sock via sudo (e.g. root)" mod:"trim"`
	Socket   string `yaml:"socket,omitempty" json:"socket,omitempty" honey:"label=Remote Docker socket" mod:"trim"`
	Platform string `yaml:"platform,omitempty" json:"platform,omitempty" honey:"label=Remote OS;enum=linux|windows" mod:"trim"`
}

// Logs holds per-command defaults for honey logs.
type Logs struct {
	Anomaly                bool    `yaml:"anomaly"                     json:"anomaly"                     honey:"label=Enable anomaly detection"`
	AnomalyModel           string  `yaml:"anomaly_model,omitempty"     json:"anomaly_model,omitempty"     honey:"label=Path to ONNX model file" mod:"trim"`
	AnomalyThresh          float64 `yaml:"anomaly_threshold,omitempty" json:"anomaly_threshold,omitempty" honey:"label=Anomaly score threshold (0–1)" default:"0.9"`
	AnomalyWindow          int     `yaml:"anomaly_window,omitempty"    json:"anomaly_window,omitempty"    honey:"label=Anomaly sliding window size"`
	AnomalyOnly            bool    `yaml:"anomaly_only"                json:"anomaly_only"                honey:"label=Only output anomalous lines"`
	AnomalyStrict          bool    `yaml:"anomaly_strict"              json:"anomaly_strict"              honey:"label=Fail if anomaly detector cannot init"`
	AnomalyTokPath         string  `yaml:"anomaly_tokenizer,omitempty"  json:"anomaly_tokenizer,omitempty"  honey:"label=Path to vocab.txt tokenizer file" mod:"trim"`
	AnomalyEndpoint        string  `yaml:"anomaly_endpoint,omitempty"      json:"anomaly_endpoint,omitempty"      honey:"label=OpenAI-compatible API URL for LLM anomaly detection (Ollama/LM Studio)" mod:"trim" default:"http://localhost:11434/v1"`
	AnomalyLLMModel        string  `yaml:"anomaly_llm_model,omitempty"     json:"anomaly_llm_model,omitempty"     honey:"label=Model name for LLM anomaly endpoint" mod:"trim" default:"llama3"`
	AnomalyContextLines    int     `yaml:"anomaly_context_lines,omitempty"    json:"anomaly_context_lines,omitempty"    honey:"label=Number of recent lines sent as context to the LLM anomaly detector" default:"5"`
	AnomalyFilterThreshold float64 `yaml:"anomaly_filter_threshold,omitempty" json:"anomaly_filter_threshold,omitempty" honey:"label=Skip LLM when fast detector score is below this value (CoLA two-tier; 0=disabled)"`
	AnomalyFreqWindow      int     `yaml:"anomaly_freq_window,omitempty"      json:"anomaly_freq_window,omitempty"      honey:"label=Short window size for rate-ratio burst detection (0=disabled, default 100)" default:"100"`
	AnomalyFreqRatio       float64 `yaml:"anomaly_freq_ratio,omitempty"       json:"anomaly_freq_ratio,omitempty"       honey:"label=Short/long rate ratio that triggers a frequency-spike anomaly (default 5.0)" default:"5.0"`
	AnomalyFeedbackFile    string  `yaml:"anomaly_feedback_file,omitempty"   json:"anomaly_feedback_file,omitempty"    honey:"label=Append scored log lines as JSONL to this file for review and threshold calibration" mod:"trim"`
	AnomalyPreprocessor    string  `yaml:"anomaly_preprocessor,omitempty"    json:"anomaly_preprocessor,omitempty"     honey:"label=Name of preprocessor to run before anomaly detection (e.g. lshd)" mod:"trim"`
	AlertEnabled           bool    `yaml:"alert_enabled"              json:"alert_enabled"              honey:"label=Alert on anomalies"`
	AlertSuppressDuration  string  `yaml:"alert_suppress_duration,omitempty" json:"alert_suppress_duration,omitempty" honey:"label=Alert suppression window (e.g. 5m)" mod:"trim"`
}

// StudioConfig holds settings for Recipe Studio UI integration.
type StudioConfig struct {
	RecipesPath string `yaml:"recipes_path,omitempty" json:"recipes_path,omitempty" honey:"label=Recipes directory" mod:"trim"`
	GitURL      string `yaml:"git_url,omitempty" json:"git_url,omitempty" honey:"label=Default Git URL" mod:"trim"`
	GitBranch   string `yaml:"git_branch,omitempty" json:"git_branch,omitempty" honey:"label=Default Git Branch" mod:"trim"`
	GitUser     string `yaml:"git_user,omitempty" json:"git_user,omitempty" honey:"label=Default Git User" mod:"trim"`
	GitPass     string `yaml:"git_pass,omitempty" json:"git_pass,omitempty" honey:"label=Default Git Pass;secret" mod:"trim"`
	GitSSH      string `yaml:"git_ssh,omitempty" json:"git_ssh,omitempty" honey:"label=Default Git SSH private key;secret" mod:"trim"`
}

// Defaults apply when CLI flags are unset.
type Defaults struct {
	SSHUser         string         `yaml:"ssh_user" json:"ssh_user" honey:"label=SSH user" mod:"trim"`
	CacheTTL        string         `yaml:"cache_ttl" json:"cache_ttl" honey:"label=Cache TTL" mod:"trim"` // e.g. "5m", "1h"
	K8sMode         string         `yaml:"k8s_mode" json:"k8s_mode" honey:"label=Kubernetes mode;enum=nodes|pods;enum_as_warning" mod:"trim"`
	K8sDebugImage   string         `yaml:"k8s_debug_image" json:"k8s_debug_image" honey:"label=Kubernetes debug image" mod:"trim"`
	CacheDir        string         `yaml:"cache_dir" json:"cache_dir" honey:"label=Cache directory" mod:"trim"`
	RecordDir       string         `yaml:"record_dir" json:"record_dir" honey:"label=Session recordings directory" mod:"trim"`
	RecordRetention string         `yaml:"record_retention" json:"record_retention" honey:"label=Auto-delete recordings older than this (e.g. 720h, 30d); empty disables" mod:"trim"`
	Output          string         `yaml:"output" json:"output" honey:"label=Output;enum=table|json|tui;enum_as_warning" mod:"trim"` // e.g. "table", "json", "tui" (default)
	Name            string         `yaml:"name" json:"name" honey:"label=Name filter" mod:"trim"`
	NameRegex       string         `yaml:"name_regex" json:"name_regex" honey:"label=Name regex" mod:"trim"`
	AISystemPrompt  string         `yaml:"ai_system_prompt" json:"ai_system_prompt" honey:"label=Default system prompt for CUE recipe ai step" mod:"trim"`
	DockerDiscover  DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover Defaults"`
	Logs            Logs           `yaml:"logs,omitempty" json:"logs,omitempty" honey:"label=Logs command defaults"`
	Studio          StudioConfig   `yaml:"studio,omitempty" json:"studio,omitempty" honey:"label=Studio defaults"`

	// secretsprovider unwraps the stack AES data key (see internal/cuetry/secrets/doc.go).
	// Examples: gcpkms://projects/…/cryptoKeys/…, awskms://, vault-transit://mount/key,
	// k8s://namespace/secret, keyring://service/user, age://, age-file://path.
	SecretsProvider string `yaml:"secretsprovider,omitempty" json:"secretsprovider,omitempty" honey:"label=Stack secrets provider URL (e.g. gcpkms://…);reserved" mod:"trim"`
	// encryptedkey is provider-specific ciphertext or field name (see secrets package doc).
	EncryptedKey string `yaml:"encryptedkey,omitempty" json:"encryptedkey,omitempty" honey:"label=Stack encrypted data key blob;secret;reserved" mod:"trim"`
}

// Backends lists optional multiple instances per provider type.
// If a slice is nil or omitted, that provider is not defined by the file (use CLI defaults).
// If a slice is non-empty, one backend is created per element.
type Backends struct {
	GCP        []GCPBackend        `yaml:"gcp" json:"gcp" honey:"label=Google Cloud;order=10" validate:"dive" mod:"dive"`
	AWS        []AWSBackend        `yaml:"aws" json:"aws" honey:"label=AWS;order=20" validate:"dive" mod:"dive"`
	Kubernetes []KubernetesBackend `yaml:"kubernetes" json:"kubernetes" honey:"label=Kubernetes;order=30" validate:"dive" mod:"dive"`
	Consul     []ConsulBackend     `yaml:"consul" json:"consul" honey:"label=Consul;order=40" validate:"dive" mod:"dive"`
	Proxmox    []ProxmoxBackend    `yaml:"proxmox" json:"proxmox" honey:"label=Proxmox;order=50" validate:"dive" mod:"dive"`
	TrueNAS    []TrueNASBackend    `yaml:"truenas" json:"truenas" honey:"label=TrueNAS;order=55" validate:"dive" mod:"dive"`
	Local      []LocalBackend      `yaml:"local" json:"local" honey:"label=Local;order=60" validate:"dive" mod:"dive"`
	Honey      []HoneyBackend      `yaml:"honey" json:"honey" honey:"label=Honey (Remote);order=65" validate:"dive" mod:"dive"`
	Docker     []DockerBackend     `yaml:"docker" json:"docker" honey:"label=Docker;order=35" validate:"dive" mod:"dive"`
}

// LocalBackend configures manually defined host lists.
type LocalBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	Hosts          []LocalHost    `yaml:"hosts" json:"hosts" honey:"label=Hosts" validate:"dive" mod:"dive"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// LocalHost represents a manually defined static server.
type LocalHost struct {
	Name      string            `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	PrimaryIP string            `yaml:"primary_ip" json:"primary_ip" honey:"label=Primary IP" validate:"required,ip" mod:"trim"`
	ExtraIPs  []string          `yaml:"extra_ips,omitempty" json:"extra_ips,omitempty" honey:"label=Extra IPs" validate:"dive,ip"`
	Zone      string            `yaml:"zone,omitempty" json:"zone,omitempty" honey:"label=Zone" mod:"trim"`
	Region    string            `yaml:"region,omitempty" json:"region,omitempty" honey:"label=Region" mod:"trim"`
	SSHUser   string            `yaml:"ssh_user,omitempty" json:"ssh_user,omitempty" honey:"label=SSH user for host" mod:"trim"`
	Meta      map[string]string `yaml:"meta,omitempty" json:"meta,omitempty" honey:"label=Metadata"`
}

// GCPBackend configures one Google Cloud Compute Engine listing.
type GCPBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	Project        string         `yaml:"project" json:"project" honey:"label=Project" validate:"required" mod:"trim"`
	Zone           string         `yaml:"zone,omitempty" json:"zone,omitempty" honey:"label=Zone" mod:"trim"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// AWSBackend configures one Amazon EC2 listing.
type AWSBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	Profile        string         `yaml:"profile" json:"profile" honey:"label=Profile" validate:"required" mod:"trim"`
	Region         string         `yaml:"region,omitempty" json:"region,omitempty" honey:"label=Region" mod:"trim"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// KubernetesBackend configures one Kubernetes nodes/pods listing.
type KubernetesBackend struct {
	Name       string `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	Context    string `yaml:"context,omitempty" json:"context,omitempty" honey:"label=Context" mod:"trim"`
	Kubeconfig string `yaml:"kubeconfig,omitempty" json:"kubeconfig,omitempty" honey:"label=Kubeconfig path" mod:"trim"`
	Mode       string `yaml:"mode" json:"mode" honey:"label=Mode;enum=nodes|pods;enum_as_warning;default=nodes" mod:"trim"`
	DebugImage string `yaml:"debug_image,omitempty" json:"debug_image,omitempty" honey:"label=Debug image" mod:"trim"`
}

// ConsulBackend configures one HashiCorp Consul catalog listing.
type ConsulBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	Addr           string         `yaml:"addr" json:"addr" honey:"label=Address" validate:"required,url" mod:"trim"`
	Datacenter     string         `yaml:"datacenter,omitempty" json:"datacenter,omitempty" honey:"label=Datacenter" mod:"trim"`
	Token          string         `yaml:"token,omitempty" json:"token,omitempty" honey:"label=Token;secret" mod:"trim"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// ProxmoxBackend configures one Proxmox VE listing.
type ProxmoxBackend struct {
	Name           string         `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	URL            string         `yaml:"url" json:"url" honey:"label=URL" validate:"required,url" mod:"trim"`
	User           string         `yaml:"user,omitempty" json:"user,omitempty" honey:"label=User" validate:"required_without=TokenID" mod:"trim"`
	Password       string         `yaml:"password,omitempty" json:"password,omitempty" honey:"label=Password;secret" validate:"required_without=TokenSecret" mod:"trim"`
	TokenID        string         `yaml:"token_id,omitempty" json:"token_id,omitempty" honey:"label=Token ID" validate:"required_without=User" mod:"trim"`
	TokenSecret    string         `yaml:"token_secret,omitempty" json:"token_secret,omitempty" honey:"label=Token secret;secret" validate:"required_without=Password" mod:"trim"`
	Insecure       bool           `yaml:"insecure" json:"insecure" honey:"label=Insecure TLS;default=false"`
	ExecMode       string         `yaml:"exec_mode,omitempty" json:"exec_mode,omitempty" honey:"label=Exec mode;enum=ssh|pve|hybrid;enum_as_warning" mod:"trim"`
	DockerDiscover DockerDiscover `yaml:"docker_discover,omitempty" json:"docker_discover,omitempty" honey:"label=Docker Auto-Discover"`
}

// TrueNASBackend configures one TrueNAS SCALE controller (WebSocket API 25.04+).
type TrueNASBackend struct {
	Name             string `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	URL              string `yaml:"url" json:"url" honey:"label=URL" validate:"required,url" mod:"trim"`
	Username         string `yaml:"username,omitempty" json:"username,omitempty" honey:"label=API key username (default root)" mod:"trim"`
	APIKey           string `yaml:"api_key" json:"api_key" honey:"label=API key;secret" validate:"required" mod:"trim"`
	Insecure         bool   `yaml:"insecure" json:"insecure" honey:"label=Insecure TLS;default=false"`
	IncludeAppliance *bool  `yaml:"include_appliance,omitempty" json:"include_appliance,omitempty" honey:"label=List appliance;default=true"`
	IncludeVMs       *bool  `yaml:"include_vms,omitempty" json:"include_vms,omitempty" honey:"label=List KVM VMs;default=true"`
	IncludeVirt      *bool  `yaml:"include_virt,omitempty" json:"include_virt,omitempty" honey:"label=List virt instances;default=true"`
	SSHUser          string `yaml:"ssh_user,omitempty" json:"ssh_user,omitempty" honey:"label=SSH user for appliance" mod:"trim"`
}

// DockerViaSSH configures an explicit SSH hop for Honey's SSH stack (not Moby ssh://).
type DockerViaSSH struct {
	Host         string `yaml:"host,omitempty" json:"host,omitempty" honey:"label=SSH host" mod:"trim"`
	Port         int    `yaml:"port,omitempty" json:"port,omitempty" honey:"label=SSH port (0 = ssh_config default)"`
	User         string `yaml:"user,omitempty" json:"user,omitempty" honey:"label=SSH user" mod:"trim"`
	IdentityFile string `yaml:"identity_file,omitempty" json:"identity_file,omitempty" honey:"label=SSH identity file" mod:"trim"`
}

// DockerBackend configures one Docker Engine API endpoint (local socket, tcp, ssh://, or Honey SSH).
type DockerBackend struct {
	Name          string       `yaml:"name" json:"name" honey:"label=Name" validate:"required" mod:"trim"`
	Host          string       `yaml:"host,omitempty" json:"host,omitempty" honey:"label=Host (unix://, tcp://, ssh://; empty = DOCKER_HOST / local socket)" mod:"trim"`
	ViaLocal      string       `yaml:"via_local,omitempty" json:"via_local,omitempty" honey:"label=Local backend name (SSH hop via backends.local)" mod:"trim"`
	ViaSSH        DockerViaSSH `yaml:"via_ssh,omitempty" json:"via_ssh,omitempty" honey:"label=SSH hop (overrides via_local when host set)"`
	Socket        string       `yaml:"socket,omitempty" json:"socket,omitempty" honey:"label=Remote Engine socket (default /var/run/docker.sock on linux)" mod:"trim"`
	Platform      string       `yaml:"platform,omitempty" json:"platform,omitempty" honey:"label=Remote OS;enum=linux|windows;enum_as_warning;default=linux" mod:"trim"`
	RunAs         string       `yaml:"run_as,omitempty" json:"run_as,omitempty" honey:"label=Remote user for docker.sock via sudo (honey-ssh only)" mod:"trim"`
	Mode          string       `yaml:"mode" json:"mode" honey:"label=Mode;enum=containers|swarm|both;enum_as_warning;default=containers" mod:"trim"`
	AllContainers bool         `yaml:"all_containers" json:"all_containers" honey:"label=Include stopped containers;default=false"`
	TLSVerify     bool         `yaml:"tls_verify" json:"tls_verify" honey:"label=Verify TLS (tcp hosts);default=true"`
	CACert        string       `yaml:"ca_cert,omitempty" json:"ca_cert,omitempty" honey:"label=CA certificate path" mod:"trim"`
	Cert          string       `yaml:"cert,omitempty" json:"cert,omitempty" honey:"label=Client certificate path" mod:"trim"`
	Key           string       `yaml:"key,omitempty" json:"key,omitempty" honey:"label=Client key path;secret" mod:"trim"`
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
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			inventoryValueDecodeHook(),
		)
	}); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	if err := finalizeAndValidate(&f); err != nil {
		return nil, fmt.Errorf("validate config %s: %w", path, err)
	}
	Set(&f)
	return &f, nil
}

func inventoryValueDecodeHook() mapstructure.DecodeHookFunc {
	valueType := reflect.TypeOf(hosts.InventoryValue{})
	return func(_, to reflect.Type, data any) (any, error) {
		if to != valueType {
			return data, nil
		}
		return hosts.NewInventoryValue(data)
	}
}

// ParseYAML parses a honey config document from memory (used by web API PUT validation).
func ParseYAML(b []byte) (*File, error) {
	var f File
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := finalizeAndValidate(&f); err != nil {
		return nil, err
	}
	return &f, nil
}

func finalizeAndValidate(f *File) error {
	if err := defaults.Set(f); err != nil {
		return fmt.Errorf("apply defaults: %w", err)
	}
	if f.Apps != nil {
		for name, app := range f.Apps {
			app.Name = name
			f.Apps[name] = app
		}
	}
	f.Sanitize()
	if err := f.Validate(); err != nil {
		return err
	}
	return nil
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
		len(f.Backends.Docker) > 0 ||
		len(f.Backends.Honey) > 0
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
	PresignedMaxSize        string `yaml:"presigned_max_size,omitempty" json:"presigned_max_size,omitempty" mod:"trim"`
	MultipartThreshold      string `yaml:"multipart_threshold,omitempty" json:"multipart_threshold,omitempty" mod:"trim"`
	PresignedURLTTL         string `yaml:"presigned_url_ttl,omitempty" json:"presigned_url_ttl,omitempty" mod:"trim"`
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
