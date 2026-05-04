package gcp

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.RegisterCRUD(gcpCRUD{})
}

type gcpCRUD struct{}

func (gcpCRUD) ID() string   { return "gcp" }
func (gcpCRUD) Name() string { return "GCP" }

func (gcpCRUD) ListOptions(cfg *config.File) []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(cfg.Backends.GCP))
	for i, b := range cfg.Backends.GCP {
		opts = append(opts, huh.NewOption(fmt.Sprintf("GCP: %s (%s)", b.Name, b.Project), fmt.Sprintf("gcp:%d", i)))
	}
	return opts
}

func (gcpCRUD) Add(cfg *config.File) error {
	var name, project, zone string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("Project").Value(&project),
			huh.NewInput().Title("Zone (optional)").Value(&zone),
		),
	).Run()
	if err == nil {
		cfg.Backends.GCP = append(cfg.Backends.GCP, config.GCPBackend{
			Name:    name,
			Project: project,
			Zone:    zone,
		})
	}
	return err
}

func (gcpCRUD) Edit(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.GCP) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.GCP[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("Project").Value(&b.Project),
			huh.NewInput().Title("Zone (optional)").Value(&b.Zone),
		),
	).Run()
	if err == nil {
		cfg.Backends.GCP[idx] = b
	}
	return err
}

func (gcpCRUD) Delete(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.GCP) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.GCP = append(cfg.Backends.GCP[:idx], cfg.Backends.GCP[idx+1:]...)
	return nil
}
