package ui

import (
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/cloudtransfer"
	"github.com/shareed2k/honey/internal/config"
)

// CloudBackendRef selects a backend entry from honey YAML for agent-transfer signing hints.
type CloudBackendRef struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Index *int   `json:"index,omitempty"`
}

// ResolveAgentTransferSigningHints loads honey config when ref is set and fills AWS/GCP signing hints
// (same semantics as the web files API). configPath is the explicit or resolved honey YAML path.
func ResolveAgentTransferSigningHints(configPath string, cloud AgentCloudBackend, ref *CloudBackendRef) (cloudtransfer.SigningHints, error) {
	var hints cloudtransfer.SigningHints
	if ref == nil {
		return hints, nil
	}
	kind := strings.ToLower(strings.TrimSpace(ref.Kind))
	if kind == "" {
		return hints, fmt.Errorf("cloud_backend_ref.kind is required")
	}
	cfgPath, err := config.ResolvePath(strings.TrimSpace(configPath))
	if err != nil {
		return hints, fmt.Errorf("resolve config path: %w", err)
	}
	if cfgPath == "" {
		return hints, fmt.Errorf("cloud_backend_ref requires a config file (set --config or HONEY_CONFIG)")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return hints, fmt.Errorf("load config: %w", err)
	}
	provider := cloudtransfer.NormalizeProvider(cloud.Provider)
	switch kind {
	case "aws":
		if provider != "" && provider != "s3" {
			return hints, fmt.Errorf("cloud_backend_ref.kind=aws requires cloud.provider=s3")
		}
		backend, err := pickAWSBackendForTransfer(cfg.Backends.AWS, ref)
		if err != nil {
			return hints, err
		}
		if p := strings.TrimSpace(backend.Profile); p != "" {
			hints.AWSProfile = p
		}
		if strings.TrimSpace(cloud.Region) == "" {
			if region := strings.TrimSpace(backend.Region); region != "" {
				hints.AWSRegion = region
			}
		}
		return hints, nil
	case "gcp", "googlecloud":
		if provider != "" && provider != "googlecloudstorage" {
			return hints, fmt.Errorf("cloud_backend_ref.kind=gcp requires cloud.provider=googlecloudstorage")
		}
		backend, err := pickGCPBackendForTransfer(cfg.Backends.GCP, ref)
		if err != nil {
			return hints, err
		}
		hints.GCPProject = strings.TrimSpace(backend.Project)
		return hints, nil
	default:
		return hints, fmt.Errorf("unsupported cloud_backend_ref.kind %q (supported: aws, gcp)", ref.Kind)
	}
}

func pickAWSBackendForTransfer(backends []config.AWSBackend, ref *CloudBackendRef) (config.AWSBackend, error) {
	if len(backends) == 0 {
		return config.AWSBackend{}, fmt.Errorf("no aws backends configured")
	}
	if ref.Index != nil {
		idx := *ref.Index
		if idx < 0 || idx >= len(backends) {
			return config.AWSBackend{}, fmt.Errorf("cloud_backend_ref.index out of range for aws backends")
		}
		return backends[idx], nil
	}
	name := strings.TrimSpace(ref.Name)
	if name != "" {
		for _, b := range backends {
			if strings.EqualFold(strings.TrimSpace(b.Name), name) {
				return b, nil
			}
		}
		return config.AWSBackend{}, fmt.Errorf("aws backend %q not found", name)
	}
	if len(backends) == 1 {
		return backends[0], nil
	}
	return config.AWSBackend{}, fmt.Errorf("multiple aws backends configured; provide cloud_backend_ref.name or index")
}

func pickGCPBackendForTransfer(backends []config.GCPBackend, ref *CloudBackendRef) (config.GCPBackend, error) {
	if len(backends) == 0 {
		return config.GCPBackend{}, fmt.Errorf("no gcp backends configured")
	}
	if ref.Index != nil {
		idx := *ref.Index
		if idx < 0 || idx >= len(backends) {
			return config.GCPBackend{}, fmt.Errorf("cloud_backend_ref.index out of range for gcp backends")
		}
		return backends[idx], nil
	}
	name := strings.TrimSpace(ref.Name)
	if name != "" {
		for _, b := range backends {
			if strings.EqualFold(strings.TrimSpace(b.Name), name) {
				return b, nil
			}
		}
		return config.GCPBackend{}, fmt.Errorf("gcp backend %q not found", name)
	}
	if len(backends) == 1 {
		return backends[0], nil
	}
	return config.GCPBackend{}, fmt.Errorf("multiple gcp backends configured; provide cloud_backend_ref.name or index")
}
