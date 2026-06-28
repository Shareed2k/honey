// Package hostapi implements shared host search and backend listing for HTTP and MCP surfaces.
package hostapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// SearchHostsInput mirrors MCP search_hosts and the web /api/v1/search JSON body.
// Overrides is an opaque map passed through to provider factories — callers do not
// need to know which fields any specific provider accepts.
type SearchHostsInput struct {
	ConfigPath string                      `json:"config_path,omitempty" mod:"trim"`
	Config     *config.File                `json:"-"` // pre-loaded config; skips config.Load when set
	Name       string                      `json:"name,omitempty"        mod:"trim"`
	NameRegex  string                      `json:"name_regex,omitempty"  mod:"trim"`
	Providers  string                      `json:"providers,omitempty"   mod:"trim"`
	Backends   string                      `json:"backends,omitempty"    mod:"trim"`
	SSHUser    string                      `json:"ssh_user,omitempty"    mod:"trim"`
	CacheTTL   string                      `json:"cache_ttl,omitempty"   mod:"trim"`
	CacheDir   string                      `json:"cache_dir,omitempty"   mod:"trim"`
	NoCache    bool                        `json:"no_cache,omitempty"`
	Refresh    bool                        `json:"refresh,omitempty"`
	Overrides  searchrun.ProviderOverrides `json:"overrides,omitempty"`
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
		if s := cfg.Defaults.Name; s != "" {
			q.NameSubstring = s
		}
	}
	if q.NameRegex == "" {
		if s := cfg.Defaults.NameRegex; s != "" {
			q.NameRegex = s
		}
	}
}

// SearchHosts runs the same search pipeline as honey search / MCP search_hosts.
func SearchHosts(ctx context.Context, in *SearchHostsInput, reg hostexec.Registry, searchReg *searchrun.Registry) (SearchHostsOutput, error) {
	out := SearchHostsOutput{Records: []hosts.Record{}}
	if in == nil {
		return out, fmt.Errorf("nil input")
	}
	cfgPath, err := config.ResolvePath(strings.TrimSpace(in.ConfigPath))
	if err != nil {
		return out, err
	}
	var cfg *config.File
	if in.Config != nil {
		cfg = in.Config
	} else if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return out, fmt.Errorf("config: %w", err)
		}
	}
	if reg != nil {
		reg.Reconfigure(cfg)
	}

	wantBackends := hosts.ParseBackendNames(in.Backends)
	q := hosts.Query{
		NameSubstring: in.Name,
		NameRegex:     in.NameRegex,
		Providers:     hosts.ParseProviders(in.Providers),
		Backends:      wantBackends,
	}
	MergeSearchDefaultsFromConfig(cfg, &q)

	provs := searchReg.BuildProviders(in.Overrides)
	if len(wantBackends) > 0 {
		provs = hosts.FilterBackendsByNames(provs, wantBackends)
		if len(provs) == 0 {
			return out, fmt.Errorf("no backends match backends=%q", in.Backends)
		}
	}

	var cacheDir string
	if cfg != nil {
		cacheDir = cfg.Defaults.CacheDir
	}
	recs, err := searchrun.RunSearch(ctx, q, provs, cacheDir, searchrun.DefaultCacheTTL, in.NoCache, in.Refresh)
	if err != nil {
		return out, err
	}
	out.Records = recs
	out.Count = len(recs)
	return out, nil
}
