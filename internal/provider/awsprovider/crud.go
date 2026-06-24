package awsprovider

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
)

type awsCRUD struct {
	cfg ConfigProvider
}

func (c awsCRUD) ID() string   { return "aws" }
func (c awsCRUD) Name() string { return "AWS" }

func (c awsCRUD) ListOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.cfg.AWSBackends()))
	for i, b := range c.cfg.AWSBackends() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("AWS: %s (%s)", b.Name, b.Profile), fmt.Sprintf("aws:%d", i)))
	}
	return opts
}

func (c awsCRUD) Add() error {
	var name, profile, region string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("Profile").Value(&profile),
			huh.NewInput().Title("Region (optional)").Value(&region),
		),
	).Run()
	if err == nil {
		c.cfg.SetAWSBackends(append(c.cfg.AWSBackends(), config.AWSBackend{
			Name:    name,
			Profile: profile,
			Region:  region,
		}))
	}
	return err
}

func (c awsCRUD) Edit(idx int) error {
	backends := c.cfg.AWSBackends()
	if idx < 0 || idx >= len(backends) {
		return fmt.Errorf("index out of bounds")
	}
	b := backends[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("Profile").Value(&b.Profile),
			huh.NewInput().Title("Region (optional)").Value(&b.Region),
		),
	).Run()
	if err == nil {
		backends[idx] = b
		c.cfg.SetAWSBackends(backends)
	}
	return err
}

func (c awsCRUD) Delete(idx int) error {
	backends := c.cfg.AWSBackends()
	if idx < 0 || idx >= len(backends) {
		return fmt.Errorf("index out of bounds")
	}
	c.cfg.SetAWSBackends(append(backends[:idx], backends[idx+1:]...))
	return nil
}
