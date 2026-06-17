package consulprovider

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.RegisterCRUD(consulCRUD{})
}

type consulCRUD struct{}

func (consulCRUD) ID() string   { return "consul" }
func (consulCRUD) Name() string { return "Consul" }

func (consulCRUD) ListOptions() []huh.Option[string] {
	cfg := config.Get()
	opts := make([]huh.Option[string], 0, len(cfg.Backends.Consul))
	for i, b := range cfg.Backends.Consul {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Consul: %s (%s)", b.Name, b.Addr), fmt.Sprintf("consul:%d", i)))
	}
	return opts
}

func (consulCRUD) Add() error {
	cfg := config.Get()
	var name, addr, datacenter, token string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("Address (e.g. 10.0.0.5:8500)").Value(&addr),
			huh.NewInput().Title("Datacenter (optional)").Value(&datacenter),
			huh.NewInput().Title("Token (optional)").EchoMode(huh.EchoModePassword).Value(&token),
		),
	).Run()
	if err == nil {
		cfg.Backends.Consul = append(cfg.Backends.Consul, config.ConsulBackend{
			Name:       name,
			Addr:       addr,
			Datacenter: datacenter,
			Token:      token,
		})
	}
	return err
}

func (consulCRUD) Edit(idx int) error {
	cfg := config.Get()
	if idx < 0 || idx >= len(cfg.Backends.Consul) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.Consul[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("Address (e.g. 10.0.0.5:8500)").Value(&b.Addr),
			huh.NewInput().Title("Datacenter (optional)").Value(&b.Datacenter),
			huh.NewInput().Title("Token (optional)").EchoMode(huh.EchoModePassword).Value(&b.Token),
		),
	).Run()
	if err == nil {
		cfg.Backends.Consul[idx] = b
	}
	return err
}

func (consulCRUD) Delete(idx int) error {
	cfg := config.Get()
	if idx < 0 || idx >= len(cfg.Backends.Consul) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.Consul = append(cfg.Backends.Consul[:idx], cfg.Backends.Consul[idx+1:]...)
	return nil
}
