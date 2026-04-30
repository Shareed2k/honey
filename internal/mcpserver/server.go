package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hostctl/internal/config"
	"hostctl/internal/hosts"
	"hostctl/internal/searchrun"
)

const serverVersion = "0.1.0"

// Run starts the MCP server on stdio until the client disconnects.
func Run(ctx context.Context) error {
	s := mcp.NewServer(&mcp.Implementation{Name: "hostctl", Version: serverVersion}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_hosts",
		Description: "Search hosts across GCP, AWS, Kubernetes, and Consul in parallel (same behavior as hostctl search). Returns JSON array of records.",
	}, handleSearchHosts)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_backends",
		Description: "List named backends from the hostctl config file (requires backends with optional name field in YAML).",
	}, handleListBackends)
	return s.Run(ctx, &mcp.StdioTransport{})
}

// --- search_hosts ---

type searchHostsInput struct {
	ConfigPath string `json:"config_path,omitempty" jsonschema:"explicit path to hostctl YAML; empty uses HOSTCTL_CONFIG or default paths"`

	Name        string `json:"name,omitempty" jsonschema:"substring filter on host/instance name"`
	NameRegex   string `json:"name_regex,omitempty" jsonschema:"regex filter on name"`
	Providers   string `json:"providers,omitempty" jsonschema:"comma-separated: gcp,aws,k8s,consul"`
	Backends    string `json:"backends,omitempty" jsonschema:"comma-separated backend names from config YAML"`
	GCPProject  string `json:"gcp_project,omitempty"`
	GCPZone     string `json:"gcp_zone,omitempty"`
	AWSProfile  string `json:"aws_profile,omitempty"`
	AWSRegion   string `json:"aws_region,omitempty"`
	KubeContext string `json:"kube_context,omitempty"`
	Kubeconfig  string `json:"kubeconfig,omitempty"`
	K8sMode     string `json:"k8s_mode,omitempty"`
	ConsulAddr  string `json:"consul_addr,omitempty"`
	ConsulDC    string `json:"consul_datacenter,omitempty"`
	ConsulToken string `json:"consul_token,omitempty"`

	CacheTTL string `json:"cache_ttl,omitempty" jsonschema:"duration e.g. 5m, 1h; empty uses config default or 1m"`
	CacheDir string `json:"cache_dir,omitempty"`
	NoCache  bool   `json:"no_cache,omitempty"`
	Refresh  bool   `json:"refresh,omitempty"`
}

type searchHostsOutput struct {
	Records []hosts.Record `json:"records"`
	Count   int            `json:"count"`
}

func handleSearchHosts(ctx context.Context, _ *mcp.CallToolRequest, in searchHostsInput) (*mcp.CallToolResult, searchHostsOutput, error) {
	cfgPath, err := config.ResolvePath(strings.TrimSpace(in.ConfigPath))
	if err != nil {
		return nil, searchHostsOutput{}, err
	}
	var cfg *config.File
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return nil, searchHostsOutput{}, fmt.Errorf("config: %w", err)
		}
	}

	q := hosts.Query{
		NameSubstring:    strings.TrimSpace(in.Name),
		NameRegex:        strings.TrimSpace(in.NameRegex),
		Providers:        hosts.ParseProviders(strings.TrimSpace(in.Providers)),
		GCPProject:       strings.TrimSpace(in.GCPProject),
		GCPZone:          strings.TrimSpace(in.GCPZone),
		AWSProfile:       strings.TrimSpace(in.AWSProfile),
		AWSRegion:        strings.TrimSpace(in.AWSRegion),
		KubeContext:      strings.TrimSpace(in.KubeContext),
		K8sMode:          strings.TrimSpace(in.K8sMode),
		ConsulAddr:       strings.TrimSpace(in.ConsulAddr),
		ConsulDatacenter: strings.TrimSpace(in.ConsulDC),
		ConsulToken:      strings.TrimSpace(in.ConsulToken),
	}
	mergeMCPDefaults(cfg, &q)

	cacheTTL := time.Minute
	if s := strings.TrimSpace(in.CacheTTL); s != "" {
		cacheTTL, err = time.ParseDuration(s)
		if err != nil {
			return nil, searchHostsOutput{}, fmt.Errorf("cache_ttl: %w", err)
		}
	} else if cfg != nil {
		if d, ok, perr := cfg.Defaults.DefaultsCacheTTL(); perr != nil {
			return nil, searchHostsOutput{}, fmt.Errorf("defaults.cache_ttl: %w", perr)
		} else if ok {
			cacheTTL = d
		}
	}

	cacheDir := strings.TrimSpace(in.CacheDir)
	if cacheDir == "" && cfg != nil {
		cacheDir = strings.TrimSpace(cfg.Defaults.CacheDir)
	}

	pf := searchrun.ProviderFlags{
		GCPProject:       strings.TrimSpace(in.GCPProject),
		GCPZone:          strings.TrimSpace(in.GCPZone),
		AWSProfile:       strings.TrimSpace(in.AWSProfile),
		AWSRegion:        strings.TrimSpace(in.AWSRegion),
		KubeContext:      strings.TrimSpace(in.KubeContext),
		K8sMode:          strings.TrimSpace(in.K8sMode),
		Kubeconfig:       strings.TrimSpace(in.Kubeconfig),
		ConsulAddr:       strings.TrimSpace(in.ConsulAddr),
		ConsulDatacenter: strings.TrimSpace(in.ConsulDC),
		ConsulToken:      strings.TrimSpace(in.ConsulToken),
	}
	provs := searchrun.BuildProviders(cfg, pf)
	want := hosts.ParseBackendNames(in.Backends)
	if len(want) > 0 {
		provs = hosts.FilterBackendsByNames(provs, want)
		if len(provs) == 0 {
			return nil, searchHostsOutput{}, fmt.Errorf("no backends match backends=%q", in.Backends)
		}
	}

	recs, err := searchrun.RunSearch(ctx, q, provs, cacheDir, cacheTTL, in.NoCache, in.Refresh)
	if err != nil {
		return nil, searchHostsOutput{}, err
	}
	out := searchHostsOutput{Records: recs, Count: len(recs)}
	return nil, out, nil
}

func mergeMCPDefaults(cfg *config.File, q *hosts.Query) {
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
}

// --- list_backends ---

type listBackendsInput struct {
	ConfigPath string `json:"config_path,omitempty" jsonschema:"explicit path to hostctl YAML; empty uses HOSTCTL_CONFIG or default paths"`
}

type backendRow struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Hint string `json:"hint,omitempty"`
}

type listBackendsOutput struct {
	Backends []backendRow `json:"backends"`
}

func handleListBackends(ctx context.Context, _ *mcp.CallToolRequest, in listBackendsInput) (*mcp.CallToolResult, listBackendsOutput, error) {
	_ = ctx
	cfgPath, err := config.ResolvePath(strings.TrimSpace(in.ConfigPath))
	if err != nil {
		return nil, listBackendsOutput{}, err
	}
	if cfgPath == "" {
		return nil, listBackendsOutput{}, fmt.Errorf("no config file found (set config_path or HOSTCTL_CONFIG or install default config)")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, listBackendsOutput{}, fmt.Errorf("config: %w", err)
	}
	if !cfg.HasAnyBackend() {
		return nil, listBackendsOutput{Backends: nil}, nil
	}
	var rows []backendRow
	for _, e := range cfg.Backends.GCP {
		rows = append(rows, backendRow{Kind: "gcp", Name: e.Name, Hint: e.Project})
	}
	for _, e := range cfg.Backends.AWS {
		rows = append(rows, backendRow{Kind: "aws", Name: e.Name, Hint: e.Profile + " " + e.Region})
	}
	for _, e := range cfg.Backends.Kubernetes {
		rows = append(rows, backendRow{Kind: "kubernetes", Name: e.Name, Hint: e.Context})
	}
	for _, e := range cfg.Backends.Consul {
		rows = append(rows, backendRow{Kind: "consul", Name: e.Name, Hint: e.Addr})
	}
	return nil, listBackendsOutput{Backends: rows}, nil
}
