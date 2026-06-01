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

const overrideKey = "k8s"

func k8sOverride(overrides searchrun.ProviderOverrides) (o config.KubernetesBackend) {
	json.Unmarshal(overrides[overrideKey], &o) //nolint:errcheck
	return o
}

// NewFactory returns a new factory for this provider.
func NewFactory() searchrun.ProviderFactory {
	return k8sFactory{}
}

type k8sFactory struct{}

func (k8sFactory) FromConfig(cfg *config.File, overrides searchrun.ProviderOverrides) []hosts.Backend {
	o := k8sOverride(overrides)
	out := make([]hosts.Backend, 0, len(cfg.Backends.Kubernetes))
	for _, e := range cfg.Backends.Kubernetes {
		kpath := searchrun.FirstNonEmpty(e.Kubeconfig, o.Kubeconfig, cliFlags.kubeconfig)
		ctx := searchrun.FirstNonEmpty(e.Context, o.Context, cliFlags.context)
		mode := searchrun.FirstNonEmpty(e.Mode, o.Mode, cliFlags.mode, strings.TrimSpace(cfg.Defaults.K8sMode))
		img := searchrun.FirstNonEmpty(e.DebugImage, o.DebugImage, cliFlags.debugImage, strings.TrimSpace(cfg.Defaults.K8sDebugImage))
		out = append(out, &K8s{Name: e.Name, KubeconfigPath: kpath, Context: ctx, Mode: mode, DebugImage: img})
	}
	return out
}

func (k8sFactory) Default(overrides searchrun.ProviderOverrides) hosts.Backend {
	o := k8sOverride(overrides)
	kpath := searchrun.FirstNonEmpty(o.Kubeconfig, cliFlags.kubeconfig)
	ctx := searchrun.FirstNonEmpty(o.Context, cliFlags.context)
	mode := searchrun.FirstNonEmpty(o.Mode, cliFlags.mode, "nodes")
	img := searchrun.FirstNonEmpty(o.DebugImage, cliFlags.debugImage)
	return &K8s{KubeconfigPath: kpath, Context: ctx, Mode: mode, DebugImage: img}
}

func (k8sFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Kubernetes))
	for _, e := range cfg.Backends.Kubernetes {
		rows = append(rows, config.BackendRow{Kind: "kubernetes", Name: e.Name, Hint: strings.TrimSpace(e.Context)})
	}
	return rows
}

func (k8sFactory) BackendKind() string { return "kubernetes" }

func (k8sFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.Kubernetes }

func (k8sFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (k8sFactory) ProviderName() string { return "k8s" }

func (k8sFactory) ExecutorFor(r hosts.Record, _ hostexec.Registry) hostexec.Executor {
	if r.Meta["kind"] == "pod" {
		return &K8sPodExecutor{}
	}
	return nil
}
