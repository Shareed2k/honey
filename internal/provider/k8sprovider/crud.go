package k8sprovider

import (
	"fmt"

	"charm.land/huh/v2"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.RegisterCRUD(k8sCRUD{})
}

type k8sCRUD struct{}

func (k8sCRUD) ID() string   { return "kubernetes" }
func (k8sCRUD) Name() string { return "Kubernetes" }

func (k8sCRUD) ListOptions() []huh.Option[string] {
	cfg := config.Get()
	opts := make([]huh.Option[string], 0, len(cfg.Backends.Kubernetes))
	for i, b := range cfg.Backends.Kubernetes {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Kubernetes: %s (%s)", b.Name, b.Context), fmt.Sprintf("kubernetes:%d", i)))
	}
	return opts
}

func (k8sCRUD) Add() error {
	cfg := config.Get()
	var name, context, kubeconfig, mode, debugImage string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("Context (optional)").Value(&context),
			huh.NewInput().Title("Kubeconfig Path (optional)").Value(&kubeconfig),
			huh.NewSelect[string]().Title("Mode").Options(huh.NewOption("Nodes", "nodes"), huh.NewOption("Pods", "pods")).Value(&mode),
			huh.NewInput().Title("Debug Image (optional)").Value(&debugImage),
		),
	).Run()
	if err == nil {
		cfg.Backends.Kubernetes = append(cfg.Backends.Kubernetes, config.KubernetesBackend{
			Name:       name,
			Context:    context,
			Kubeconfig: kubeconfig,
			Mode:       mode,
			DebugImage: debugImage,
		})
	}
	return err
}

func (k8sCRUD) Edit(idx int) error {
	cfg := config.Get()
	if idx < 0 || idx >= len(cfg.Backends.Kubernetes) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.Kubernetes[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("Context (optional)").Value(&b.Context),
			huh.NewInput().Title("Kubeconfig Path (optional)").Value(&b.Kubeconfig),
			huh.NewSelect[string]().Title("Mode").Options(huh.NewOption("Nodes", "nodes"), huh.NewOption("Pods", "pods")).Value(&b.Mode),
			huh.NewInput().Title("Debug Image (optional)").Value(&b.DebugImage),
		),
	).Run()
	if err == nil {
		cfg.Backends.Kubernetes[idx] = b
	}
	return err
}

func (k8sCRUD) Delete(idx int) error {
	cfg := config.Get()
	if idx < 0 || idx >= len(cfg.Backends.Kubernetes) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.Kubernetes = append(cfg.Backends.Kubernetes[:idx], cfg.Backends.Kubernetes[idx+1:]...)
	return nil
}
