package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/ui"
)

var (
	flagName               string
	flagNameRegex          string
	flagProviders          string
	flagOutput             string
	flagNoUI               bool
	flagJSON               bool
	flagSSHUser            string
	flagCacheTTL           time.Duration
	flagNoCache            bool
	flagRefresh            bool
	flagCacheDir           string
	flagGCPProject         string
	flagGCPZone            string
	flagAWSProfile         string
	flagAWSRegion          string
	flagKubeContext        string
	flagK8sMode            string
	flagK8sDebugImg        string
	flagConsulAddr         string
	flagConsulDC           string
	flagConsulToken        string
	flagKubeconfig         string
	flagConfig             string
	flagBackends           string
	flagProxmoxURL         string
	flagProxmoxUser        string
	flagProxmoxPassword    string
	flagProxmoxTokenID     string
	flagProxmoxTokenSecret string
	flagProxmoxInsecure    bool
)

var searchCmd = &cobra.Command{
	Use:   "search [name]",
	Short: "Search instances across providers in parallel",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSearch,
}

func init() {
	searchCmd.Flags().StringVar(&flagConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)")
	searchCmd.Flags().StringVar(&flagName, "name", "", "Substring filter on instance/node/pod name (case-insensitive)")
	searchCmd.Flags().StringVar(&flagNameRegex, "name-regex", "", "Regex filter on name (overrides --name substring)")
	searchCmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul,proxmox (default: all)")
	searchCmd.Flags().StringVar(&flagBackends, "backends", "", "Comma-separated backend names (YAML backends.*.name); only those entries run")
	searchCmd.Flags().StringVarP(&flagOutput, "output", "o", "tui", "Output format: tui, table, json")
	searchCmd.Flags().BoolVar(&flagNoUI, "no-ui", false, "Skip interactive UI (same as --output=json)")
	searchCmd.Flags().BoolVar(&flagJSON, "json", false, "Print results as JSON (same as --output=json)")
	searchCmd.Flags().StringVar(&flagSSHUser, "ssh-user", os.Getenv("USER"), "Default SSH user for connect actions")

	searchCmd.Flags().StringVar(&flagGCPProject, "gcp-project", "", "GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)")
	searchCmd.Flags().StringVar(&flagGCPZone, "gcp-zone", "", "Limit GCP to a single zone (default: all zones)")

	searchCmd.Flags().StringVar(&flagAWSProfile, "aws-profile", "", "AWS shared config profile")
	searchCmd.Flags().StringVar(&flagAWSRegion, "aws-region", "", "AWS region (default: from profile/env)")

	searchCmd.Flags().StringVar(&flagKubeContext, "kube-context", "", "Kubernetes context override")
	searchCmd.Flags().StringVar(&flagKubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	searchCmd.Flags().StringVar(&flagK8sMode, "k8s-mode", "nodes", "Kubernetes search mode: nodes or pods")
	searchCmd.Flags().StringVar(&flagK8sDebugImg, "k8s-debug-image", "", "Container image used for ephemeral debug containers (default: alpine:3.23)")

	searchCmd.Flags().StringVar(&flagConsulAddr, "consul-addr", "", "Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)")
	searchCmd.Flags().StringVar(&flagConsulDC, "consul-datacenter", "", "Consul datacenter")
	searchCmd.Flags().StringVar(&flagConsulToken, "consul-token", "", "Consul ACL token (or CONSUL_HTTP_TOKEN)")

	searchCmd.Flags().StringVar(&flagProxmoxURL, "proxmox-url", "", "Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)")
	searchCmd.Flags().StringVar(&flagProxmoxUser, "proxmox-user", "", "Proxmox user (e.g. root@pam)")
	searchCmd.Flags().StringVar(&flagProxmoxPassword, "proxmox-password", "", "Proxmox password")
	searchCmd.Flags().StringVar(&flagProxmoxTokenID, "proxmox-token-id", "", "Proxmox token ID (e.g. root@pam!token)")
	searchCmd.Flags().StringVar(&flagProxmoxTokenSecret, "proxmox-token-secret", "", "Proxmox token secret")
	searchCmd.Flags().BoolVar(&flagProxmoxInsecure, "proxmox-insecure", false, "Skip TLS verification for Proxmox")
}

// runSearchCore runs the same search pipeline as search (flags, config, cache,
// providers). queryArgs are optional positional tokens: if exactly one is
// passed and name filters are empty, it becomes the name substring filter.
// The returned configPath is the resolved honey YAML path (may be empty).
func runSearchCore(cmd *cobra.Command, queryArgs []string) ([]hosts.Record, string, *config.File, string, error) {
	q := hosts.Query{
		NameSubstring:      flagName,
		NameRegex:          flagNameRegex,
		Providers:          hosts.ParseProviders(flagProviders),
		GCPProject:         flagGCPProject,
		GCPZone:            flagGCPZone,
		AWSProfile:         flagAWSProfile,
		AWSRegion:          flagAWSRegion,
		KubeContext:        flagKubeContext,
		K8sMode:            flagK8sMode,
		K8sDebugImage:      flagK8sDebugImg,
		ConsulAddr:         flagConsulAddr,
		ConsulDatacenter:   flagConsulDC,
		ConsulToken:        flagConsulToken,
		ProxmoxURL:         flagProxmoxURL,
		ProxmoxUser:        flagProxmoxUser,
		ProxmoxPassword:    flagProxmoxPassword,
		ProxmoxTokenID:     flagProxmoxTokenID,
		ProxmoxTokenSecret: flagProxmoxTokenSecret,
		ProxmoxInsecure:    flagProxmoxInsecure,
	}

	cfgPath, err := config.ResolvePath(flagConfig)
	if err != nil {
		return nil, "", nil, "", err
	}
	var cfg *config.File
	if cfgPath != "" {
		cfg, err = config.Load(cfgPath)
		if err != nil {
			return nil, "", nil, cfgPath, fmt.Errorf("config: %w", err)
		}
	}
	hostexec.ReconfigureFromHoneyConfig(cfg)

	cacheTTL := flagCacheTTL
	cacheDir := flagCacheDir
	sshUser := flagSSHUser
	if cfg != nil {
		if d, ok, perr := cfg.Defaults.DefaultsCacheTTL(); perr != nil {
			return nil, "", nil, cfgPath, fmt.Errorf("defaults.cache_ttl: %w", perr)
		} else if ok && !rootPersistentFlagChanged(cmd, "cache-ttl") {
			cacheTTL = d
		}
		if s := strings.TrimSpace(cfg.Defaults.CacheDir); s != "" && !rootPersistentFlagChanged(cmd, "cache-dir") {
			cacheDir = s
		}
		if s := strings.TrimSpace(cfg.Defaults.SSHUser); s != "" && !cmd.Flags().Changed("ssh-user") {
			sshUser = s
		}
		if s := strings.TrimSpace(cfg.Defaults.K8sMode); s != "" && !cmd.Flags().Changed("k8s-mode") {
			q.K8sMode = s
		}
		if s := strings.TrimSpace(cfg.Defaults.K8sDebugImage); s != "" && !cmd.Flags().Changed("k8s-debug-image") {
			q.K8sDebugImage = s
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
			return nil, "", nil, cfgPath, fmt.Errorf("no backends match --backends %q: set name on each backends.* list entry in config (unnamed backends are ignored by this filter)", flagBackends)
		}
	}
	ctx := context.Background()

	records, err := searchrun.RunSearch(ctx, q, provs, cacheDir, cacheTTL, flagNoCache, flagRefresh)
	if err != nil {
		return nil, "", nil, cfgPath, err
	}
	return records, sshUser, cfg, cfgPath, nil
}

func runSearch(cmd *cobra.Command, args []string) error {
	records, sshUser, cfg, cfgPath, err := runSearchCore(cmd, args)
	if err != nil {
		return err
	}

	if flagJSON || flagNoUI {
		flagOutput = "json"
	}
	if cfg != nil && !cmd.Flags().Changed("output") && !cmd.Flags().Changed("json") && !cmd.Flags().Changed("no-ui") {
		if s := strings.TrimSpace(cfg.Defaults.Output); s != "" {
			flagOutput = s
		}
	}

	switch flagOutput {
	case "json":
		if records == nil {
			records = make([]hosts.Record, 0)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	case "table":
		return ui.PrintStaticTable(records)
	default:
		recordDir := config.ResolveRecordDir(cfg, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
		recordOnStart := recordDirFlagChanged(cmd) && strings.TrimSpace(flagRecordDir) != ""
		return ui.RunTable(records, sshUser, ui.RunTableOptions{
			RecordDir:     recordDir,
			RecordEnabled: recordOnStart,
			Config:        cfg,
			ConfigPath:    cfgPath,
		})
	}
}
