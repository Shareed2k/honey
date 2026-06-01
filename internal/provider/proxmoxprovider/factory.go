package proxmoxprovider

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

const overrideKey = "proxmox"

func proxmoxOverride(overrides searchrun.ProviderOverrides) (o config.ProxmoxBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

func init() {
	searchrun.Register(proxmoxFactory{})
}

type proxmoxFactory struct{}

func (proxmoxFactory) FromConfig(cfg *config.File, overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := proxmoxOverride(overrides)
	out := make([]hosts.Backend, 0, len(cfg.Backends.Proxmox))
	for _, e := range cfg.Backends.Proxmox {
		url := searchrun.FirstNonEmpty(e.URL, o.URL, cliFlags.url)
		user := searchrun.FirstNonEmpty(e.User, o.User, cliFlags.user)
		pass := searchrun.FirstNonEmpty(e.Password, o.Password, cliFlags.password)
		tid := searchrun.FirstNonEmpty(e.TokenID, o.TokenID, cliFlags.tokenID)
		tsec := searchrun.FirstNonEmpty(e.TokenSecret, o.TokenSecret, cliFlags.tokenSecret)
		insecure := e.Insecure || o.Insecure || cliFlags.insecure
		execMode := strings.ToLower(strings.TrimSpace(e.ExecMode))
		switch execMode {
		case "pve", "hybrid":
		default:
			execMode = "ssh"
		}
		b := searchrun.WithDockerDiscover(
			&Proxmox{
				Name:        e.Name,
				URL:         url,
				User:        user,
				Password:    pass,
				TokenID:     tid,
				TokenSecret: tsec,
				Insecure:    insecure,
				ExecMode:    execMode,
			},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (proxmoxFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := proxmoxOverride(overrides)
	url := searchrun.FirstNonEmpty(o.URL, cliFlags.url)
	user := searchrun.FirstNonEmpty(o.User, cliFlags.user)
	pass := searchrun.FirstNonEmpty(o.Password, cliFlags.password)
	tid := searchrun.FirstNonEmpty(o.TokenID, cliFlags.tokenID)
	tsec := searchrun.FirstNonEmpty(o.TokenSecret, cliFlags.tokenSecret)
	insecure := o.Insecure || cliFlags.insecure
	return searchrun.WithDockerDiscover(
		&Proxmox{
			URL:         url,
			User:        user,
			Password:    pass,
			TokenID:     tid,
			TokenSecret: tsec,
			Insecure:    insecure,
		},
		config.DockerDiscover{},
	)
}

func (proxmoxFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Proxmox))
	for _, e := range cfg.Backends.Proxmox {
		rows = append(rows, config.BackendRow{Kind: "proxmox", Name: e.Name, Hint: strings.TrimSpace(e.URL)})
	}
	return rows
}

func (proxmoxFactory) BackendKind() string { return "proxmox" }

func (proxmoxFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.Proxmox }

func (proxmoxFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (proxmoxFactory) ProviderName() string { return "proxmox" }

func (proxmoxFactory) ExecutorFor(r hosts.Record) hostexec.Executor {
	return resolveProxmoxExecutor(r)
}

func (proxmoxFactory) ReconfigureFromConfig(cfg *config.File) { reconfigureProxmox(cfg) }
