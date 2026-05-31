package truenasprovider

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(truenasFactory{})
}

type truenasFactory struct{}

func (truenasFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	out := make([]hosts.Backend, 0, len(cfg.Backends.TrueNAS))
	for _, e := range cfg.Backends.TrueNAS {
		url := e.URL
		if url == "" {
			url = f.TrueNASURL
		}
		if url == "" {
			url = cliFlags.url
		}
		apiKey := firstNonEmpty(e.APIKey, f.TrueNASAPIKey, cliFlags.apiKey, os.Getenv("TRUENAS_API_KEY"))
		user := e.Username
		if user == "" {
			user = f.TrueNASUser
		}
		if user == "" {
			user = cliFlags.user
		}
		insecure := e.Insecure || f.TrueNASInsecure || cliFlags.insecure
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

func (truenasFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	url := f.TrueNASURL
	if url == "" {
		url = cliFlags.url
	}
	user := f.TrueNASUser
	if user == "" {
		user = cliFlags.user
	}
	return &TrueNAS{
		URL:              url,
		Username:         user,
		APIKey:           firstNonEmpty(f.TrueNASAPIKey, cliFlags.apiKey, os.Getenv("TRUENAS_API_KEY")),
		Insecure:         f.TrueNASInsecure || cliFlags.insecure,
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

func (truenasFactory) ExecutorFor(r hosts.Record) hostexec.Executor {
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
