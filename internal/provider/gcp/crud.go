package gcp

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
)

type gcpCRUD struct {
	cfg ConfigProvider
}

func (c gcpCRUD) ID() string   { return "gcp" }
func (c gcpCRUD) Name() string { return "GCP" }

func (c gcpCRUD) ListOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.cfg.GCPBackends()))
	for i, b := range c.cfg.GCPBackends() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("GCP: %s (%s)", b.Name, b.Project), fmt.Sprintf("gcp:%d", i)))
	}
	return opts
}

func (c gcpCRUD) Add() error {
	var name, project, zone string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("Project").Value(&project),
			huh.NewInput().Title("Zone (optional)").Value(&zone),
		),
	).Run()
	if err == nil {
		c.cfg.SetGCPBackends(append(c.cfg.GCPBackends(), config.GCPBackend{
			Name:    name,
			Project: project,
			Zone:    zone,
		}))
	}
	return err
}

func (c gcpCRUD) Edit(idx int) error {
	if idx < 0 || idx >= len(c.cfg.GCPBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	b := c.cfg.GCPBackends()[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("Project").Value(&b.Project),
			huh.NewInput().Title("Zone (optional)").Value(&b.Zone),
		),
	).Run()
	if err == nil {
		backends := c.cfg.GCPBackends()
		backends[idx] = b
		c.cfg.SetGCPBackends(backends)
	}
	return err
}

func (c gcpCRUD) Delete(idx int) error {
	if idx < 0 || idx >= len(c.cfg.GCPBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	backends := c.cfg.GCPBackends()
	c.cfg.SetGCPBackends(append(backends[:idx], backends[idx+1:]...))
	return nil
}
