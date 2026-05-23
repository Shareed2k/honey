// Package hostapi implements shared host search and backend listing for HTTP and MCP surfaces.
package hostapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// SearchHostsInput mirrors MCP search_hosts and the web /api/v1/search JSON body.
type SearchHostsInput struct {
	ConfigPath          string `json:"config_path,omitempty"`
	Name                string `json:"name,omitempty"`
	NameRegex           string `json:"name_regex,omitempty"`
	Providers           string `json:"providers,omitempty"`
	Backends            string `json:"backends,omitempty"`
	GCPProject          string `json:"gcp_project,omitempty"`
	GCPZone             string `json:"gcp_zone,omitempty"`
	AWSProfile          string `json:"aws_profile,omitempty"`
	AWSRegion           string `json:"aws_region,omitempty"`
	KubeContext         string `json:"kube_context,omitempty"`
	Kubeconfig          string `json:"kubeconfig,omitempty"`
	K8sMode             string `json:"k8s_mode,omitempty"`
	K8sDebugImage       string `json:"k8s_debug_image,omitempty"`
	ConsulAddr          string `json:"consul_addr,omitempty"`
	ConsulDC            string `json:"consul_datacenter,omitempty"`
	ConsulToken         string `json:"consul_token,omitempty"`
	ProxmoxURL          string `json:"proxmox_url,omitempty"`
	ProxmoxUser         string `json:"proxmox_user,omitempty"`
	ProxmoxPassword     string `json:"proxmox_password,omitempty"`
	ProxmoxTokenID      string `json:"proxmox_token_id,omitempty"`
	ProxmoxTokenSecret  string `json:"proxmox_token_secret,omitempty"`
	ProxmoxInsecure     bool   `json:"proxmox_insecure,omitempty"`
	TrueNASURL          string `json:"truenas_url,omitempty"`
	TrueNASUser         string `json:"truenas_user,omitempty"`
	TrueNASAPIKey       string `json:"truenas_api_key,omitempty"`
	TrueNASInsecure     bool   `json:"truenas_insecure,omitempty"`
	DockerHost          string `json:"docker_host,omitempty"`
	DockerMode          string `json:"docker_mode,omitempty"`
	DockerAllContainers bool   `json:"docker_all_containers,omitempty"`
	DockerViaLocal      string `json:"docker_via_local,omitempty"`
	DockerViaSSHHost    string `json:"docker_via_ssh_host,omitempty"`
	DockerSocket        string `json:"docker_socket,omitempty"`
	DockerPlatform      string `json:"docker_platform,omitempty"`
	SSHUser             string `json:"ssh_user,omitempty"`
	CacheTTL            string `json:"cache_ttl,omitempty"`
	CacheDir            string `json:"cache_dir,omitempty"`
	NoCache             bool   `json:"no_cache,omitempty"`
	Refresh             bool   `json:"refresh,omitempty"`
}

// SearchHostsOutput is the JSON search result.
type SearchHostsOutput struct {
	Records []hosts.Record `json:"records"`
	Count   int            `json:"count"`
}

// MergeSearchDefaultsFromConfig applies config defaults to q when fields are empty.
func MergeSearchDefaultsFromConfig(cfg *config.File, q *hosts.Query) {
	if cfg == nil {
		return
	}
	if q.NameSubstring == "" {
		if s := strings.TrimSpace(cfg.Defaults.Name); s != "" {
			q.NameSubstring = s
		}
	}
	if q.NameRegex == "" {
		if s := strings.TrimSpace(cfg.Defaults.NameRegex); s != "" {
			q.NameRegex = s
		}
	}
	if q.K8sMode == "" {
		if s := strings.TrimSpace(cfg.Defaults.K8sMode); s != "" {
			q.K8sMode = s
		}
	}
	if q.K8sDebugImage == "" {
		if s := strings.TrimSpace(cfg.Defaults.K8sDebugImage); s != "" {
			q.K8sDebugImage = s
		}
	}
}

// SearchHosts runs the same search pipeline as honey search / MCP search_hosts.
func SearchHosts(ctx context.Context, in *SearchHostsInput) (SearchHostsOutput, error) {
	out := SearchHostsOutput{Records: []hosts.Record{}}
	if in == nil {
		return out, fmt.Errorf("nil input")
	}
	cfgPath, err := config.ResolvePath(strings.TrimSpace(in.ConfigPath))
	if err != nil {
		return out, err
	}
	var cfg *config.File
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return out, fmt.Errorf("config: %w", err)
		}
	}
	hostexec.ReconfigureFromHoneyConfig(cfg)

	q := hosts.Query{
		NameSubstring:       strings.TrimSpace(in.Name),
		NameRegex:           strings.TrimSpace(in.NameRegex),
		Providers:           hosts.ParseProviders(strings.TrimSpace(in.Providers)),
		GCPProject:          strings.TrimSpace(in.GCPProject),
		GCPZone:             strings.TrimSpace(in.GCPZone),
		AWSProfile:          strings.TrimSpace(in.AWSProfile),
		AWSRegion:           strings.TrimSpace(in.AWSRegion),
		KubeContext:         strings.TrimSpace(in.KubeContext),
		K8sMode:             strings.TrimSpace(in.K8sMode),
		K8sDebugImage:       strings.TrimSpace(in.K8sDebugImage),
		ConsulAddr:          strings.TrimSpace(in.ConsulAddr),
		ConsulDatacenter:    strings.TrimSpace(in.ConsulDC),
		ConsulToken:         strings.TrimSpace(in.ConsulToken),
		ProxmoxURL:          strings.TrimSpace(in.ProxmoxURL),
		ProxmoxUser:         strings.TrimSpace(in.ProxmoxUser),
		ProxmoxPassword:     strings.TrimSpace(in.ProxmoxPassword),
		ProxmoxTokenID:      strings.TrimSpace(in.ProxmoxTokenID),
		ProxmoxTokenSecret:  strings.TrimSpace(in.ProxmoxTokenSecret),
		ProxmoxInsecure:     in.ProxmoxInsecure,
		TrueNASURL:          strings.TrimSpace(in.TrueNASURL),
		TrueNASUser:         strings.TrimSpace(in.TrueNASUser),
		TrueNASAPIKey:       strings.TrimSpace(in.TrueNASAPIKey),
		TrueNASInsecure:     in.TrueNASInsecure,
		DockerHost:          strings.TrimSpace(in.DockerHost),
		DockerMode:          strings.TrimSpace(in.DockerMode),
		DockerAllContainers: in.DockerAllContainers,
		DockerViaLocal:      strings.TrimSpace(in.DockerViaLocal),
		DockerViaSSHHost:    strings.TrimSpace(in.DockerViaSSHHost),
		DockerSocket:        strings.TrimSpace(in.DockerSocket),
		DockerPlatform:      strings.TrimSpace(in.DockerPlatform),
		DockerSSHUser:       strings.TrimSpace(in.SSHUser),
	}
	MergeSearchDefaultsFromConfig(cfg, &q)

	cacheTTL := searchrun.DefaultCacheTTL
	if s := strings.TrimSpace(in.CacheTTL); s != "" {
		cacheTTL, err = time.ParseDuration(s)
		if err != nil {
			return out, fmt.Errorf("cache_ttl: %w", err)
		}
	} else if cfg != nil {
		if d, ok, perr := cfg.Defaults.DefaultsCacheTTL(); perr != nil {
			return out, fmt.Errorf("defaults.cache_ttl: %w", perr)
		} else if ok {
			cacheTTL = d
		}
	}

	cacheDir := strings.TrimSpace(in.CacheDir)
	if cacheDir == "" && cfg != nil {
		cacheDir = strings.TrimSpace(cfg.Defaults.CacheDir)
	}

	pf := searchrun.ProviderFlags{
		GCPProject:          strings.TrimSpace(in.GCPProject),
		GCPZone:             strings.TrimSpace(in.GCPZone),
		AWSProfile:          strings.TrimSpace(in.AWSProfile),
		AWSRegion:           strings.TrimSpace(in.AWSRegion),
		KubeContext:         strings.TrimSpace(in.KubeContext),
		K8sMode:             strings.TrimSpace(in.K8sMode),
		Kubeconfig:          strings.TrimSpace(in.Kubeconfig),
		ConsulAddr:          strings.TrimSpace(in.ConsulAddr),
		ConsulDatacenter:    strings.TrimSpace(in.ConsulDC),
		ConsulToken:         strings.TrimSpace(in.ConsulToken),
		ProxmoxURL:          strings.TrimSpace(in.ProxmoxURL),
		ProxmoxUser:         strings.TrimSpace(in.ProxmoxUser),
		ProxmoxPassword:     strings.TrimSpace(in.ProxmoxPassword),
		ProxmoxTokenID:      strings.TrimSpace(in.ProxmoxTokenID),
		ProxmoxTokenSecret:  strings.TrimSpace(in.ProxmoxTokenSecret),
		ProxmoxInsecure:     in.ProxmoxInsecure,
		TrueNASURL:          strings.TrimSpace(in.TrueNASURL),
		TrueNASUser:         strings.TrimSpace(in.TrueNASUser),
		TrueNASAPIKey:       strings.TrimSpace(in.TrueNASAPIKey),
		TrueNASInsecure:     in.TrueNASInsecure,
		DockerHost:          strings.TrimSpace(in.DockerHost),
		DockerMode:          strings.TrimSpace(in.DockerMode),
		DockerAllContainers: in.DockerAllContainers,
		DockerViaLocal:      strings.TrimSpace(in.DockerViaLocal),
		DockerViaSSHHost:    strings.TrimSpace(in.DockerViaSSHHost),
		DockerSocket:        strings.TrimSpace(in.DockerSocket),
		DockerPlatform:      strings.TrimSpace(in.DockerPlatform),
	}
	provs := searchrun.BuildProviders(cfg, pf)
	want := hosts.ParseBackendNames(in.Backends)
	if len(want) > 0 {
		provs = hosts.FilterBackendsByNames(provs, want)
		if len(provs) == 0 {
			return out, fmt.Errorf("no backends match backends=%q", in.Backends)
		}
	}

	recs, err := searchrun.RunSearch(ctx, q, provs, cacheDir, cacheTTL, in.NoCache, in.Refresh)
	if err != nil {
		return out, err
	}
	out.Records = recs
	out.Count = len(recs)
	return out, nil
}
