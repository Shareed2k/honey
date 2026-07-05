package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/inventory"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/ui"
	"github.com/spf13/cobra"
)

var (
	flagName      string
	flagNameRegex string
	flagProviders string
	flagOutput    string
	flagNoUI      bool
	flagJSON      bool
	flagSSHUser   string
	flagCacheTTL  time.Duration
	flagNoCache   bool
	flagRefresh   bool
	flagCacheDir  string
	flagBackends  string
	flagFilters   []string
)

var searchCmd = &cobra.Command{
	Use:   "search [name]",
	Short: "Search instances across providers in parallel",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSearch,
}

func init() {
	addSearchCoreFlags(searchCmd)
	searchCmd.Flags().StringVarP(&flagOutput, "output", "o", "tui", "Output format: tui, table, json")
	searchCmd.Flags().BoolVar(&flagNoUI, "no-ui", false, "Skip interactive UI (same as --output=json)")
	searchCmd.Flags().BoolVar(&flagJSON, "json", false, "Print results as JSON (same as --output=json)")
}

func addSearchCoreFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&flagName, "name", "", "Substring filter on instance/node/pod name (case-insensitive)")
	cmd.Flags().StringVar(&flagNameRegex, "name-regex", "", "Regex filter on name (overrides --name substring)")
	cmd.Flags().StringVar(&flagProviders, "provider", "", "Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)")
	cmd.Flags().StringVar(&flagBackends, "backends", "", "Comma-separated backend names (YAML backends.*.name); only those entries run")
	cmd.Flags().StringArrayVar(&flagFilters, "filter", nil, "Post-discovery filter (repeatable: group:web, var:service=nginx)")
	cmd.Flags().StringVar(&flagSSHUser, "ssh-user", "", "Default SSH user for connect actions (defaults to config or OS user)")

	GetSearchRegistry().RegisterAllProviderFlags(cmd)
}

// runSearchCore runs the same search pipeline as search (flags, config, cache,
// providers). queryArgs are optional positional tokens: if exactly one is
// passed and name filters are empty, it becomes the name substring filter.
// The returned configPath is the resolved honey YAML path (may be empty).
func runSearchCore(cmd *cobra.Command, queryArgs []string) ([]hosts.Record, string, *config.File, string, error) {
	q := hosts.Query{
		NameSubstring: flagName,
		NameRegex:     flagNameRegex,
		Providers:     hosts.ParseProviders(flagProviders),
	}

	cfgPath := resolvedCfgPath
	cfg := resolvedCfg
	reg := buildHostExecRegistry()
	reg.Reconfigure(cfg)

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
		if s := strings.TrimSpace(cfg.Defaults.Name); s != "" && !cmd.Flags().Changed("name") && q.NameSubstring == "" {
			q.NameSubstring = s
		}
		if s := strings.TrimSpace(cfg.Defaults.NameRegex); s != "" && !cmd.Flags().Changed("name-regex") && q.NameRegex == "" {
			q.NameRegex = s
		}
	}

	filters := append([]string(nil), flagFilters...)
	if len(queryArgs) == 1 && q.NameSubstring == "" && q.NameRegex == "" {
		if inventory.IsFilterToken(queryArgs[0]) {
			filters = append(filters, queryArgs[0])
		} else {
			q.NameSubstring = queryArgs[0]
		}
	}

	provs := GetSearchRegistry().BuildProviders(nil)
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
	if cfg != nil {
		if err := inventory.Apply(records, cfg.Inventory); err != nil {
			return nil, "", nil, cfgPath, fmt.Errorf("apply inventory: %w", err)
		}
	}
	records, err = inventory.FilterRecords(records, filters)
	if err != nil {
		return nil, "", nil, cfgPath, err
	}
	return records, sshUser, cfg, cfgPath, nil
}

func runSearch(cmd *cobra.Command, args []string) error {
	clientCache := engine.NewClientCache()

	records, sshUser, cfg, cfgPath, err := runSearchCore(cmd, args)
	if err != nil {
		clientCache.CloseAll()
		return err
	}

	reg := buildHostExecRegistry()
	reg.Reconfigure(cfg)
	clientCache.SetRegistry(reg)

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
		return engine.PrintStaticTable(records)
	default:
		recordDir := config.ResolveRecordDir(cfg, cfgPath, flagRecordDir, recordDirFlagChanged(cmd))
		recordOnStart := recordDirFlagChanged(cmd) && strings.TrimSpace(flagRecordDir) != ""
		return ui.RunTable(records, sshUser, ui.RunTableOptions{
			RecordDir:     recordDir,
			RecordEnabled: recordOnStart,
			Config:        cfg,
			ConfigPath:    cfgPath,
			ClientCache:   clientCache,
			ExecRegistry:  reg,
		})
	}
}
