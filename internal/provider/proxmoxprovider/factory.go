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

// ConfigProvider defines the configuration dependency required by this provider.
type ConfigProvider interface {
	ProxmoxBackends() []config.ProxmoxBackend
	ProxmoxBackendSlicePtr() *[]config.ProxmoxBackend
	SetProxmoxBackends([]config.ProxmoxBackend)
	DockerDiscover() config.DockerDiscover
}

const overrideKey = "proxmox"

func proxmoxOverride(overrides searchrun.ProviderOverrides) (o config.ProxmoxBackend) {
	if len(overrides[overrideKey]) > 0 {
		_ = json.Unmarshal(overrides[overrideKey], &o) // overrides are optional
	}
	return o
}

// NewFactory returns a new factory for this provider.
func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(proxmoxCRUD{cfg: cfg})
	return proxmoxFactory{cfg: cfg}
}

type proxmoxFactory struct {
	cfg ConfigProvider
}

func (f proxmoxFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := proxmoxOverride(overrides)
	out := make([]hosts.Backend, 0, len(f.cfg.ProxmoxBackends()))
	for _, e := range f.cfg.ProxmoxBackends() {
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
			searchrun.MergeDockerDiscover(f.cfg.DockerDiscover(), e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (f proxmoxFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
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

func (f proxmoxFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.ProxmoxBackends()))
	for _, e := range f.cfg.ProxmoxBackends() {
		rows = append(rows, config.BackendRow{Kind: "proxmox", Name: e.Name, Hint: strings.TrimSpace(e.URL)})
	}
	return rows
}

func (f proxmoxFactory) BackendKind() string { return "proxmox" }

func (f proxmoxFactory) BackendSlicePtr() any {
	return f.cfg.ProxmoxBackendSlicePtr()
}

func (f proxmoxFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (f proxmoxFactory) ProviderName() string { return "proxmox" }

func (f proxmoxFactory) ExecutorFor(r hosts.Record, _ hostexec.Registry) hostexec.Executor {
	return resolveProxmoxExecutor(r)
}

func (f proxmoxFactory) ReconfigureFromConfig() {
	reconfigureProxmox()
}
