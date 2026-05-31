package k8sprovider

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(k8sFactory{})
}

type k8sFactory struct{}

func (k8sFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	out := make([]hosts.Backend, 0, len(cfg.Backends.Kubernetes))
	for _, e := range cfg.Backends.Kubernetes {
		kpath, ctx, mode, img := e.Kubeconfig, e.Context, e.Mode, e.DebugImage
		if kpath == "" {
			kpath = f.Kubeconfig
		}
		if kpath == "" {
			kpath = cliFlags.kubeconfig
		}
		if ctx == "" {
			ctx = f.KubeContext
		}
		if ctx == "" {
			ctx = cliFlags.context
		}
		if mode == "" {
			mode = f.K8sMode
		}
		if mode == "" {
			mode = cliFlags.mode
		}
		if img == "" {
			img = f.K8sDebugImage
		}
		if img == "" {
			img = cliFlags.debugImage
		}
		out = append(out, &K8s{Name: e.Name, KubeconfigPath: kpath, Context: ctx, Mode: mode, DebugImage: img})
	}
	return out
}

func (k8sFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	kpath := f.Kubeconfig
	if kpath == "" {
		kpath = cliFlags.kubeconfig
	}
	ctx := f.KubeContext
	if ctx == "" {
		ctx = cliFlags.context
	}
	mode := f.K8sMode
	if mode == "" {
		mode = cliFlags.mode
	}
	if mode == "" {
		mode = "nodes"
	}
	img := f.K8sDebugImage
	if img == "" {
		img = cliFlags.debugImage
	}
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

func (k8sFactory) ExecutorFor(r hosts.Record) hostexec.Executor {
	if r.Meta["kind"] == "pod" {
		return &K8sPodExecutor{}
	}
	return nil
}
