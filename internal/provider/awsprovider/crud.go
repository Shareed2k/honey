package awsprovider

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.RegisterCRUD(awsCRUD{})
}

type awsCRUD struct{}

func (awsCRUD) ID() string   { return "aws" }
func (awsCRUD) Name() string { return "AWS" }

func (awsCRUD) ListOptions(cfg *config.File) []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(cfg.Backends.AWS))
	for i, b := range cfg.Backends.AWS {
		opts = append(opts, huh.NewOption(fmt.Sprintf("AWS: %s (%s)", b.Name, b.Profile), fmt.Sprintf("aws:%d", i)))
	}
	return opts
}

func (awsCRUD) Add(cfg *config.File) error {
	var name, profile, region string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("Profile").Value(&profile),
			huh.NewInput().Title("Region (optional)").Value(&region),
		),
	).Run()
	if err == nil {
		cfg.Backends.AWS = append(cfg.Backends.AWS, config.AWSBackend{
			Name:    name,
			Profile: profile,
			Region:  region,
		})
	}
	return err
}

func (awsCRUD) Edit(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.AWS) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.AWS[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("Profile").Value(&b.Profile),
			huh.NewInput().Title("Region (optional)").Value(&b.Region),
		),
	).Run()
	if err == nil {
		cfg.Backends.AWS[idx] = b
	}
	return err
}

func (awsCRUD) Delete(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.AWS) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.AWS = append(cfg.Backends.AWS[:idx], cfg.Backends.AWS[idx+1:]...)
	return nil
}
