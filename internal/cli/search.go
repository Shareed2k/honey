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
	flagTrueNASURL         string
	flagTrueNASUser        string
	flagTrueNASAPIKey      string
	flagTrueNASInsecure    bool
	flagDockerHost         string
	flagDockerMode         string
	flagDockerAll          bool
	flagDockerViaLocal     string
	flagDockerViaSSHHost   string
	flagDockerSocket       string
	flagDockerPlatform     string
)

var searchCmd = &cobra.Command{
	Use:   "search [name]",
	Short: "Search instances across providers in parallel",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSearch,
}

func init() {
	addSearchCoreFlags(searchCmd, "nodes")
	searchCmd.Flags().StringVarP(&flagOutput, "output", "o", "tui", "Output format: tui, table, json")
	searchCmd.Flags().BoolVar(&flagNoUI, "no-ui", false, "Skip interactive UI (same as --output=json)")
	searchCmd.Flags().BoolVar(&flagJSON, "json", false, "Print results as JSON (same as --output=json)")
}

func addSearchCoreFlags(cmd *cobra.Command, k8sModeDefault string) {
	cmd.Flags().StringVar(&flagConfig, "config", "", "Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)")
	cmd.Flags().StringVar(&flagName, "name", "", "Substring filter on instance/node/pod name (case-insensitive)")
	cmd.Flags().StringVar(&flagNameRegex, "name-regex", "", "Regex filter on name (overrides --name substring)")
	cmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)")
	cmd.Flags().StringVar(&flagBackends, "backends", "", "Comma-separated backend names (YAML backends.*.name); only those entries run")
	cmd.Flags().StringVar(&flagSSHUser, "ssh-user", "", "Default SSH user for connect actions (defaults to config or OS user)")

	cmd.Flags().StringVar(&flagGCPProject, "gcp-project", "", "GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)")
	cmd.Flags().StringVar(&flagGCPZone, "gcp-zone", "", "Limit GCP to a single zone (default: all zones)")

	cmd.Flags().StringVar(&flagAWSProfile, "aws-profile", "", "AWS shared config profile")
	cmd.Flags().StringVar(&flagAWSRegion, "aws-region", "", "AWS region (default: from profile/env)")

	cmd.Flags().StringVar(&flagKubeContext, "kube-context", "", "Kubernetes context override")
	cmd.Flags().StringVar(&flagKubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	cmd.Flags().StringVar(&flagK8sMode, "k8s-mode", k8sModeDefault, "Kubernetes search mode: nodes or pods")
	cmd.Flags().StringVar(&flagK8sDebugImg, "k8s-debug-image", "", "Container image used for ephemeral debug containers (default: alpine:3.23)")

	cmd.Flags().StringVar(&flagConsulAddr, "consul-addr", "", "Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)")
	cmd.Flags().StringVar(&flagConsulDC, "consul-datacenter", "", "Consul datacenter")
	cmd.Flags().StringVar(&flagConsulToken, "consul-token", "", "Consul ACL token (or CONSUL_HTTP_TOKEN)")

	cmd.Flags().StringVar(&flagProxmoxURL, "proxmox-url", "", "Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)")
	cmd.Flags().StringVar(&flagProxmoxUser, "proxmox-user", "", "Proxmox user (e.g. root@pam)")
	cmd.Flags().StringVar(&flagProxmoxPassword, "proxmox-password", "", "Proxmox password")
	cmd.Flags().StringVar(&flagProxmoxTokenID, "proxmox-token-id", "", "Proxmox token ID (e.g. root@pam!token)")
	cmd.Flags().StringVar(&flagProxmoxTokenSecret, "proxmox-token-secret", "", "Proxmox token secret")
	cmd.Flags().BoolVar(&flagProxmoxInsecure, "proxmox-insecure", false, "Skip TLS verification for Proxmox")

	cmd.Flags().StringVar(&flagTrueNASURL, "truenas-url", "", "TrueNAS SCALE URL (https://host or wss://host/api/current)")
	cmd.Flags().StringVar(&flagTrueNASUser, "truenas-user", "", "TrueNAS API key username (default root)")
	cmd.Flags().StringVar(&flagTrueNASAPIKey, "truenas-api-key", "", "TrueNAS API key (or TRUENAS_API_KEY)")
	cmd.Flags().BoolVar(&flagTrueNASInsecure, "truenas-insecure", false, "Skip TLS verification for TrueNAS")

	cmd.Flags().StringVar(&flagDockerHost, "docker-host", "", "Docker host (unix://, tcp://, ssh://; default: DOCKER_HOST / local socket)")
	cmd.Flags().StringVar(&flagDockerMode, "docker-mode", "containers", "Docker search mode: containers, swarm, or both")
	cmd.Flags().BoolVar(&flagDockerAll, "docker-all", false, "Include stopped containers in docker search")
	cmd.Flags().StringVar(&flagDockerViaLocal, "docker-via-local", "", "Docker via Honey SSH: backends.local name")
	cmd.Flags().StringVar(&flagDockerViaSSHHost, "docker-via-ssh-host", "", "Docker via Honey SSH: explicit host")
	cmd.Flags().StringVar(&flagDockerSocket, "docker-socket", "", "Remote Docker socket (default /var/run/docker.sock on linux)")
	cmd.Flags().StringVar(&flagDockerPlatform, "docker-platform", "linux", "Remote Docker host OS: linux or windows")
}

// runSearchCore runs the same search pipeline as search (flags, config, cache,
// providers). queryArgs are optional positional tokens: if exactly one is
// passed and name filters are empty, it becomes the name substring filter.
// The returned configPath is the resolved honey YAML path (may be empty).
func runSearchCore(cmd *cobra.Command, queryArgs []string) ([]hosts.Record, string, *config.File, string, error) {
	q := hosts.Query{
		NameSubstring:       flagName,
		NameRegex:           flagNameRegex,
		Providers:           hosts.ParseProviders(flagProviders),
		GCPProject:          flagGCPProject,
		GCPZone:             flagGCPZone,
		AWSProfile:          flagAWSProfile,
		AWSRegion:           flagAWSRegion,
		KubeContext:         flagKubeContext,
		K8sMode:             flagK8sMode,
		K8sDebugImage:       flagK8sDebugImg,
		ConsulAddr:          flagConsulAddr,
		ConsulDatacenter:    flagConsulDC,
		ConsulToken:         flagConsulToken,
		ProxmoxURL:          flagProxmoxURL,
		ProxmoxUser:         flagProxmoxUser,
		ProxmoxPassword:     flagProxmoxPassword,
		ProxmoxTokenID:      flagProxmoxTokenID,
		ProxmoxTokenSecret:  flagProxmoxTokenSecret,
		ProxmoxInsecure:     flagProxmoxInsecure,
		TrueNASURL:          flagTrueNASURL,
		TrueNASUser:         flagTrueNASUser,
		TrueNASAPIKey:       flagTrueNASAPIKey,
		TrueNASInsecure:     flagTrueNASInsecure,
		DockerHost:          flagDockerHost,
		DockerMode:          flagDockerMode,
		DockerAllContainers: flagDockerAll,
		DockerViaLocal:      flagDockerViaLocal,
		DockerViaSSHHost:    flagDockerViaSSHHost,
		DockerSocket:        flagDockerSocket,
		DockerPlatform:      flagDockerPlatform,
		DockerSSHUser:       flagSSHUser,
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
	clientCache := ui.NewClientCache()
	ui.SetDockerSSHBorrowCache(clientCache)

	records, sshUser, cfg, cfgPath, err := runSearchCore(cmd, args)
	if err != nil {
		clientCache.CloseAll()
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
		defer clientCache.CloseAll()
		if records == nil {
			records = make([]hosts.Record, 0)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	case "table":
		defer clientCache.CloseAll()
		return ui.PrintStaticTable(records)
	default:
		recordDir := config.ResolveRecordDir(cfg, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
		recordOnStart := recordDirFlagChanged(cmd) && strings.TrimSpace(flagRecordDir) != ""
		return ui.RunTable(records, sshUser, ui.RunTableOptions{
			RecordDir:     recordDir,
			RecordEnabled: recordOnStart,
			Config:        cfg,
			ConfigPath:    cfgPath,
			ClientCache:   clientCache,
		})
	}
}
