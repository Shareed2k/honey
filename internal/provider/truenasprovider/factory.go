package truenasprovider

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

const overrideKey = "truenas"

func truenasOverride(overrides searchrun.ProviderOverrides) (o config.TrueNASBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider.
func NewFactory() searchrun.ProviderFactory {
	return truenasFactory{}
}

type truenasFactory struct{}

func (truenasFactory) FromConfig(cfg *config.File, overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := truenasOverride(overrides)
	out := make([]hosts.Backend, 0, len(cfg.Backends.TrueNAS))
	for _, e := range cfg.Backends.TrueNAS {
		url := searchrun.FirstNonEmpty(e.URL, o.URL, cliFlags.url)
		apiKey := firstNonEmpty(e.APIKey, o.APIKey, cliFlags.apiKey, os.Getenv("TRUENAS_API_KEY"))
		user := searchrun.FirstNonEmpty(e.Username, o.Username, cliFlags.user)
		insecure := e.Insecure || o.Insecure || cliFlags.insecure
		out = append(out, &TrueNAS{
			Name:             e.Name,
			URL:              url,
			Username:         user,
			APIKey:           apiKey,
			Insecure:         insecure,
			IncludeAppliance: boolDefault(e.IncludeAppliance, true),
			IncludeVMs:       boolDefault(e.IncludeVMs, true),
			IncludeVirt:      boolDefault(e.IncludeVirt, true),
			SSHUser:          strings.TrimSpace(e.SSHUser),
		})
	}
	return out
}

func (truenasFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := truenasOverride(overrides)
	url := searchrun.FirstNonEmpty(o.URL, cliFlags.url)
	user := searchrun.FirstNonEmpty(o.Username, cliFlags.user)
	return &TrueNAS{
		URL:              url,
		Username:         user,
		APIKey:           firstNonEmpty(o.APIKey, cliFlags.apiKey, os.Getenv("TRUENAS_API_KEY")),
		Insecure:         o.Insecure || cliFlags.insecure,
		IncludeAppliance: true,
		IncludeVMs:       true,
		IncludeVirt:      true,
	}
}

func (truenasFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.TrueNAS))
	for _, e := range cfg.Backends.TrueNAS {
		rows = append(rows, config.BackendRow{Kind: "truenas", Name: e.Name, Hint: strings.TrimSpace(e.URL)})
	}
	return rows
}

func (truenasFactory) BackendKind() string { return "truenas" }

func (truenasFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.TrueNAS }

func (truenasFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (truenasFactory) ProviderName() string { return "truenas" }

func (truenasFactory) ExecutorFor(r hosts.Record, _ hostexec.Registry) hostexec.Executor {
	if TruenasTunnelUsesAPIShell(r) {
		return APIShellExecutor()
	}
	return nil
}

func (truenasFactory) ReconfigureFromConfig(cfg *config.File) { reconfigureTrueNAS(cfg) }

func boolDefault(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
