package consulprovider

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
)

type consulCRUD struct {
	cfg ConfigProvider
}

func (c consulCRUD) ID() string   { return "consul" }
func (c consulCRUD) Name() string { return "Consul" }

func (c consulCRUD) ListOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.cfg.ConsulBackends()))
	for i, b := range c.cfg.ConsulBackends() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Consul: %s (%s)", b.Name, b.Addr), fmt.Sprintf("consul:%d", i)))
	}
	return opts
}

func (c consulCRUD) Add() error {
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
		c.cfg.SetConsulBackends(append(c.cfg.ConsulBackends(), config.ConsulBackend{
			Name:       name,
			Addr:       addr,
			Datacenter: datacenter,
			Token:      token,
		}))
	}
	return err
}

func (c consulCRUD) Edit(idx int) error {
	if idx < 0 || idx >= len(c.cfg.ConsulBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	b := c.cfg.ConsulBackends()[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("Address (e.g. 10.0.0.5:8500)").Value(&b.Addr),
			huh.NewInput().Title("Datacenter (optional)").Value(&b.Datacenter),
			huh.NewInput().Title("Token (optional)").EchoMode(huh.EchoModePassword).Value(&b.Token),
		),
	).Run()
	if err == nil {
		backends := c.cfg.ConsulBackends()
		backends[idx] = b
		c.cfg.SetConsulBackends(backends)
	}
	return err
}

func (c consulCRUD) Delete(idx int) error {
	if idx < 0 || idx >= len(c.cfg.ConsulBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	backends := c.cfg.ConsulBackends()
	c.cfg.SetConsulBackends(append(backends[:idx], backends[idx+1:]...))
	return nil
}
