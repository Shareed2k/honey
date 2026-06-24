package k8sprovider

import (
	"fmt"

	"charm.land/huh/v2"

	"github.com/shareed2k/honey/internal/config"
)

type k8sCRUD struct {
	cfg ConfigProvider
}

func (c k8sCRUD) ID() string   { return "kubernetes" }
func (c k8sCRUD) Name() string { return "Kubernetes" }

func (c k8sCRUD) ListOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.cfg.KubernetesBackends()))
	for i, b := range c.cfg.KubernetesBackends() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Kubernetes: %s (%s)", b.Name, b.Context), fmt.Sprintf("kubernetes:%d", i)))
	}
	return opts
}

func (c k8sCRUD) Add() error {
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
		c.cfg.SetKubernetesBackends(append(c.cfg.KubernetesBackends(), config.KubernetesBackend{
			Name:       name,
			Context:    context,
			Kubeconfig: kubeconfig,
			Mode:       mode,
			DebugImage: debugImage,
		}))
	}
	return err
}

func (c k8sCRUD) Edit(idx int) error {
	if idx < 0 || idx >= len(c.cfg.KubernetesBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	b := c.cfg.KubernetesBackends()[idx]
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
		backends := c.cfg.KubernetesBackends()
		backends[idx] = b
		c.cfg.SetKubernetesBackends(backends)
	}
	return err
}

func (c k8sCRUD) Delete(idx int) error {
	if idx < 0 || idx >= len(c.cfg.KubernetesBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	backends := c.cfg.KubernetesBackends()
	c.cfg.SetKubernetesBackends(append(backends[:idx], backends[idx+1:]...))
	return nil
}
