package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"hostctl/internal/hosts"
	"hostctl/internal/provider/awsprovider"
	"hostctl/internal/provider/consulprovider"
	"hostctl/internal/provider/gcp"
	"hostctl/internal/provider/k8sprovider"
	"hostctl/internal/ui"
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
)

var searchCmd = &cobra.Command{
	Use:   "search [name]",
	Short: "Search instances across providers in parallel",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&flagName, "name", "", "Substring filter on instance/node/pod name (case-insensitive)")
	searchCmd.Flags().StringVar(&flagNameRegex, "name-regex", "", "Regex filter on name (overrides --name substring)")
	searchCmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul (default: all)")
	searchCmd.Flags().BoolVar(&flagNoUI, "no-ui", false, "Skip interactive UI")
	searchCmd.Flags().BoolVar(&flagJSON, "json", false, "Print results as JSON (implies --no-ui)")
	searchCmd.Flags().StringVar(&flagSSHUser, "ssh-user", os.Getenv("USER"), "Default SSH user for connect actions")
	searchCmd.Flags().DurationVar(&flagCacheTTL, "cache-ttl", time.Minute, "Cache time-to-live")
	searchCmd.Flags().BoolVar(&flagNoCache, "no-cache", false, "Bypass read/write cache")
	searchCmd.Flags().BoolVar(&flagRefresh, "refresh", false, "Ignore cached entries and refresh")
	searchCmd.Flags().StringVar(&flagCacheDir, "cache-dir", "", "Override cache directory (default: XDG_CACHE_HOME/hostctl)")

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

func runSearch(cmd *cobra.Command, args []string) error {
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
	if len(args) == 1 && q.NameSubstring == "" && q.NameRegex == "" {
		q.NameSubstring = args[0]
	}

	provs := buildProviders()
	ctx := context.Background()

	cacheDir := flagCacheDir
	if cacheDir == "" {
		d, err := hosts.DefaultCacheDir()
		if err != nil {
			return err
		}
		cacheDir = d
	}
	cachePath := filepath.Join(cacheDir, "cache.json")
	var fc *hosts.FileCache
	if !flagNoCache {
		fc = hosts.NewFileCache(cachePath, flagCacheTTL)
	}

	records, err := hosts.RunParallel(ctx, q, provs, fc, flagNoCache, flagRefresh, hosts.DefaultCacheKey)
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

	return ui.RunTable(records, flagSSHUser)
}

func buildProviders() []hosts.Backend {
	return []hosts.Backend{
		&gcp.GCP{Project: flagGCPProject, Zone: flagGCPZone},
		&awsprovider.AWS{Profile: flagAWSProfile, Region: flagAWSRegion},
		&k8sprovider.K8s{KubeconfigPath: flagKubeconfig},
		&consulprovider.Consul{},
	}
}
