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

// ConfigProvider defines the configuration dependency required by this provider.
type ConfigProvider interface {
	TrueNASBackends() []config.TrueNASBackend
	TrueNASBackendSlicePtr() *[]config.TrueNASBackend
	SetTrueNASBackends([]config.TrueNASBackend)
	DockerDiscover() config.DockerDiscover
}

const overrideKey = "truenas"

func truenasOverride(overrides searchrun.ProviderOverrides) (o config.TrueNASBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider. tunnel/dialer (implemented in
// the ui package) are injected so resolver-created API-shell executors can run the
// port-forward and proxy upstream dial.
func NewFactory(tunnel TunnelRunner, dialer UpstreamDialer, cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(truenasCRUD{cfg: cfg})
	return truenasFactory{tunnel: tunnel, dialer: dialer, cfg: cfg}
}

type truenasFactory struct {
	tunnel TunnelRunner
	dialer UpstreamDialer
	cfg    ConfigProvider
}

func (f truenasFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := truenasOverride(overrides)
	out := make([]hosts.Backend, 0, len(f.cfg.TrueNASBackends()))
	for _, e := range f.cfg.TrueNASBackends() {
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

func (f truenasFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
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

func (f truenasFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.TrueNASBackends()))
	for _, e := range f.cfg.TrueNASBackends() {
		rows = append(rows, config.BackendRow{Kind: "truenas", Name: e.Name, Hint: strings.TrimSpace(e.URL)})
	}
	return rows
}

func (f truenasFactory) BackendKind() string { return "truenas" }

func (f truenasFactory) BackendSlicePtr() any {
	return f.cfg.TrueNASBackendSlicePtr()
}

func (f truenasFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (f truenasFactory) ProviderName() string { return "truenas" }

func (f truenasFactory) ExecutorFor(r hosts.Record, _ hostexec.Registry) hostexec.Executor {
	if TruenasTunnelUsesAPIShell(r) {
		return NewAPIShellExecutor(f.tunnel, f.dialer)
	}
	return nil
}

func (f truenasFactory) ReconfigureFromConfig() {
	reconfigureTrueNAS()
}

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
