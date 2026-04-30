package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"honey/internal/config"
	"honey/internal/hosts"
	"honey/internal/searchrun"
	"honey/internal/ui"
)

var (
	flagName        string
	flagNameRegex   string
	flagProviders   string
	flagNoUI        bool
	flagJSON        bool
	flagSSHUser     string
	flagCacheTTL    time.Duration
	flagNoCache     bool
	flagRefresh     bool
	flagCacheDir    string
	flagGCPProject  string
	flagGCPZone     string
	flagAWSProfile  string
	flagAWSRegion   string
	flagKubeContext string
	flagK8sMode     string
	flagConsulAddr  string
	flagConsulDC    string
	flagConsulToken string
	flagKubeconfig  string
	flagConfig      string
	flagBackends    string
)

var searchCmd = &cobra.Command{
	Use:   "search [name]",
	Short: "Search instances across providers in parallel",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&flagConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG / HOSTCTL_CONFIG or default paths in README)")
	searchCmd.Flags().StringVar(&flagName, "name", "", "Substring filter on instance/node/pod name (case-insensitive)")
	searchCmd.Flags().StringVar(&flagNameRegex, "name-regex", "", "Regex filter on name (overrides --name substring)")
	searchCmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul (default: all)")
	searchCmd.Flags().StringVar(&flagBackends, "backends", "", "Comma-separated backend names (YAML backends.*.name); only those entries run")
	searchCmd.Flags().BoolVar(&flagNoUI, "no-ui", false, "Skip interactive UI")
	searchCmd.Flags().BoolVar(&flagJSON, "json", false, "Print results as JSON (implies --no-ui)")
	searchCmd.Flags().StringVar(&flagSSHUser, "ssh-user", os.Getenv("USER"), "Default SSH user for connect actions")
	searchCmd.Flags().DurationVar(&flagCacheTTL, "cache-ttl", time.Minute, "Cache time-to-live")
	searchCmd.Flags().BoolVar(&flagNoCache, "no-cache", false, "Bypass read/write cache")
	searchCmd.Flags().BoolVar(&flagRefresh, "refresh", false, "Ignore cached entries and refresh")
	searchCmd.Flags().StringVar(&flagCacheDir, "cache-dir", "", "Override cache directory (default: XDG_CACHE_HOME/honey)")

	searchCmd.Flags().StringVar(&flagGCPProject, "gcp-project", "", "GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)")
	searchCmd.Flags().StringVar(&flagGCPZone, "gcp-zone", "", "Limit GCP to a single zone (default: all zones)")

	searchCmd.Flags().StringVar(&flagAWSProfile, "aws-profile", "", "AWS shared config profile")
	searchCmd.Flags().StringVar(&flagAWSRegion, "aws-region", "", "AWS region (default: from profile/env)")

	searchCmd.Flags().StringVar(&flagKubeContext, "kube-context", "", "Kubernetes context override")
	searchCmd.Flags().StringVar(&flagKubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	searchCmd.Flags().StringVar(&flagK8sMode, "k8s-mode", "nodes", "Kubernetes search mode: nodes or pods")

	searchCmd.Flags().StringVar(&flagConsulAddr, "consul-addr", "", "Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)")
	searchCmd.Flags().StringVar(&flagConsulDC, "consul-datacenter", "", "Consul datacenter")
	searchCmd.Flags().StringVar(&flagConsulToken, "consul-token", "", "Consul ACL token (or CONSUL_HTTP_TOKEN)")
}

// runSearchCore runs the same search pipeline as search (flags, config, cache,
// providers). queryArgs are optional positional tokens: if exactly one is
// passed and name filters are empty, it becomes the name substring filter.
func runSearchCore(cmd *cobra.Command, queryArgs []string) ([]hosts.Record, string, error) {
	q := hosts.Query{
		NameSubstring:    flagName,
		NameRegex:        flagNameRegex,
		Providers:        hosts.ParseProviders(flagProviders),
		GCPProject:       flagGCPProject,
		GCPZone:          flagGCPZone,
		AWSProfile:       flagAWSProfile,
		AWSRegion:        flagAWSRegion,
		KubeContext:      flagKubeContext,
		K8sMode:          flagK8sMode,
		ConsulAddr:       flagConsulAddr,
		ConsulDatacenter: flagConsulDC,
		ConsulToken:      flagConsulToken,
	}

	cfgPath, err := config.ResolvePath(flagConfig)
	if err != nil {
		return nil, "", err
	}
	var cfg *config.File
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return nil, "", fmt.Errorf("config: %w", err)
		}
	}

	cacheTTL := flagCacheTTL
	cacheDir := flagCacheDir
	sshUser := flagSSHUser
	if cfg != nil {
		if d, ok, perr := cfg.Defaults.DefaultsCacheTTL(); perr != nil {
			return nil, "", fmt.Errorf("defaults.cache_ttl: %w", perr)
		} else if ok && !cmd.Flags().Changed("cache-ttl") {
			cacheTTL = d
		}
		if s := strings.TrimSpace(cfg.Defaults.CacheDir); s != "" && !cmd.Flags().Changed("cache-dir") {
			cacheDir = s
		}
		if s := strings.TrimSpace(cfg.Defaults.SSHUser); s != "" && !cmd.Flags().Changed("ssh-user") {
			sshUser = s
		}
		if s := strings.TrimSpace(cfg.Defaults.K8sMode); s != "" && !cmd.Flags().Changed("k8s-mode") {
			q.K8sMode = s
		}
		if s := strings.TrimSpace(cfg.Defaults.Name); s != "" && !cmd.Flags().Changed("name") && q.NameSubstring == "" {
			q.NameSubstring = s
		}
		if s := strings.TrimSpace(cfg.Defaults.NameRegex); s != "" && !cmd.Flags().Changed("name-regex") && q.NameRegex == "" {
			q.NameRegex = s
		}
	}

	if len(queryArgs) == 1 && q.NameSubstring == "" && q.NameRegex == "" {
		q.NameSubstring = queryArgs[0]
	}

	provs := buildProviders(cfg)
	wantBackends := hosts.ParseBackendNames(flagBackends)
	if len(wantBackends) > 0 {
		provs = hosts.FilterBackendsByNames(provs, wantBackends)
		if len(provs) == 0 {
			return nil, "", fmt.Errorf("no backends match --backends %q: set name on each backends.* list entry in config (unnamed backends are ignored by this filter)", flagBackends)
		}
	}
	ctx := context.Background()

	records, err := searchrun.RunSearch(ctx, q, provs, cacheDir, cacheTTL, flagNoCache, flagRefresh)
	if err != nil {
		return nil, "", err
	}
	return records, sshUser, nil
}

func runSearch(cmd *cobra.Command, args []string) error {
	records, sshUser, err := runSearchCore(cmd, args)
	if err != nil {
		return err
	}

	if flagJSON {
		flagNoUI = true
	}
	if flagJSON || flagNoUI {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	}

	return ui.RunTable(records, sshUser)
}

