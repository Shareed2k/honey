package truenasprovider

import (
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/config"
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
		apiKey := firstNonEmpty(e.APIKey, f.TrueNASAPIKey, os.Getenv("TRUENAS_API_KEY"))
		user := e.Username
		if user == "" {
			user = f.TrueNASUser
		}
		insecure := e.Insecure || f.TrueNASInsecure
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
	return &TrueNAS{
		URL:              f.TrueNASURL,
		Username:         f.TrueNASUser,
		APIKey:           firstNonEmpty(f.TrueNASAPIKey, os.Getenv("TRUENAS_API_KEY")),
		Insecure:         f.TrueNASInsecure,
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
