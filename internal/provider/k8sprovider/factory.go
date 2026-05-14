package k8sprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/config"
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
		if ctx == "" {
			ctx = f.KubeContext
		}
		if mode == "" {
			mode = f.K8sMode
		}
		if img == "" {
			img = f.K8sDebugImage
		}
		out = append(out, &K8s{Name: e.Name, KubeconfigPath: kpath, Context: ctx, Mode: mode, DebugImage: img})
	}
	return out
}

func (k8sFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	mode := f.K8sMode
	if mode == "" {
		mode = "nodes"
	}
	return &K8s{KubeconfigPath: f.Kubeconfig, Context: f.KubeContext, Mode: mode, DebugImage: f.K8sDebugImage}
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
