package k8sprovider

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
	KubernetesBackends() []config.KubernetesBackend
	KubernetesBackendSlicePtr() *[]config.KubernetesBackend
	SetKubernetesBackends([]config.KubernetesBackend)
	K8sMode() string
	K8sDebugImage() string
}

const overrideKey = "k8s"

func k8sOverride(overrides searchrun.ProviderOverrides) (o config.KubernetesBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider. interactive (implemented in
// the ui package) is injected so resolver-created executors can run TTY sessions.
func NewFactory(interactive InteractiveRunner, cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(k8sCRUD{cfg: cfg})
	return k8sFactory{interactive: interactive, cfg: cfg}
}

type k8sFactory struct {
	interactive InteractiveRunner
	cfg         ConfigProvider
}

func (f k8sFactory) FromConfig(overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := k8sOverride(overrides)
	out := make([]hosts.Backend, 0, len(f.cfg.KubernetesBackends()))
	for _, e := range f.cfg.KubernetesBackends() {
		kpath := searchrun.FirstNonEmpty(e.Kubeconfig, o.Kubeconfig, cliFlags.kubeconfig)
		ctx := searchrun.FirstNonEmpty(e.Context, o.Context, cliFlags.context)
		mode := searchrun.FirstNonEmpty(e.Mode, o.Mode, cliFlags.mode, strings.TrimSpace(f.cfg.K8sMode()))
		img := searchrun.FirstNonEmpty(e.DebugImage, o.DebugImage, cliFlags.debugImage, strings.TrimSpace(f.cfg.K8sDebugImage()))
		out = append(out, &K8s{Name: e.Name, KubeconfigPath: kpath, Context: ctx, Mode: mode, DebugImage: img})
	}
	return out
}

func (f k8sFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := k8sOverride(overrides)
	kpath := searchrun.FirstNonEmpty(o.Kubeconfig, cliFlags.kubeconfig)
	ctx := searchrun.FirstNonEmpty(o.Context, cliFlags.context)
	mode := searchrun.FirstNonEmpty(o.Mode, cliFlags.mode, "nodes")
	img := searchrun.FirstNonEmpty(o.DebugImage, cliFlags.debugImage)
	return &K8s{KubeconfigPath: kpath, Context: ctx, Mode: mode, DebugImage: img}
}

func (f k8sFactory) BackendRows() []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(f.cfg.KubernetesBackends()))
	for _, e := range f.cfg.KubernetesBackends() {
		rows = append(rows, config.BackendRow{Kind: "kubernetes", Name: e.Name, Hint: strings.TrimSpace(e.Context)})
	}
	return rows
}

func (f k8sFactory) BackendKind() string { return "kubernetes" }

func (f k8sFactory) BackendSlicePtr() any {
	return f.cfg.KubernetesBackendSlicePtr()
}

func (f k8sFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (f k8sFactory) ProviderName() string { return "k8s" }

func (f k8sFactory) ExecutorFor(r hosts.Record, _ hostexec.Registry) hostexec.Executor {
	if r.Meta["kind"] == "pod" {
		return &K8sPodExecutor{interactive: f.interactive}
	}
	return nil
}
