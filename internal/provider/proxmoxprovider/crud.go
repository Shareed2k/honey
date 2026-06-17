package proxmoxprovider

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
)

type proxmoxCRUD struct {
	cfg ConfigProvider
}

func (c proxmoxCRUD) ID() string   { return "proxmox" }
func (c proxmoxCRUD) Name() string { return "Proxmox" }

func (c proxmoxCRUD) ListOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.cfg.ProxmoxBackends()))
	for i, b := range c.cfg.ProxmoxBackends() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Proxmox: %s (%s)", b.Name, b.URL), fmt.Sprintf("proxmox:%d", i)))
	}
	return opts
}

func (c proxmoxCRUD) Add() error {
	var name, url, user, password, tokenID, tokenSecret, execMode string
	var insecure bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("URL (e.g. https://10.0.0.10:8006/api2/json)").Value(&url),
			huh.NewInput().Title("User (optional)").Value(&user),
			huh.NewInput().Title("Password (optional)").EchoMode(huh.EchoModePassword).Value(&password),
			huh.NewInput().Title("Token ID (optional)").Value(&tokenID),
			huh.NewInput().Title("Token Secret (optional)").EchoMode(huh.EchoModePassword).Value(&tokenSecret),
			huh.NewConfirm().Title("Insecure (Skip TLS Verify)?").Value(&insecure),
			huh.NewSelect[string]().Title("Exec mode (optional)").
				Options(
					huh.NewOption("default (unset)", ""),
					huh.NewOption("ssh", "ssh"),
					huh.NewOption("pve", "pve"),
					huh.NewOption("hybrid", "hybrid"),
				).
				Value(&execMode),
		),
	).Run()
	if err == nil {
		c.cfg.SetProxmoxBackends(append(c.cfg.ProxmoxBackends(), config.ProxmoxBackend{
			Name:        name,
			URL:         url,
			User:        user,
			Password:    password,
			TokenID:     tokenID,
			TokenSecret: tokenSecret,
			Insecure:    insecure,
			ExecMode:    execMode,
		}))
	}
	return err
}

func (c proxmoxCRUD) Edit(idx int) error {
	if idx < 0 || idx >= len(c.cfg.ProxmoxBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	b := c.cfg.ProxmoxBackends()[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("URL (e.g. https://10.0.0.10:8006/api2/json)").Value(&b.URL),
			huh.NewInput().Title("User (optional)").Value(&b.User),
			huh.NewInput().Title("Password (optional)").EchoMode(huh.EchoModePassword).Value(&b.Password),
			huh.NewInput().Title("Token ID (optional)").Value(&b.TokenID),
			huh.NewInput().Title("Token Secret (optional)").EchoMode(huh.EchoModePassword).Value(&b.TokenSecret),
			huh.NewConfirm().Title("Insecure (Skip TLS Verify)?").Value(&b.Insecure),
			huh.NewSelect[string]().Title("Exec mode (optional)").
				Options(
					huh.NewOption("default (unset)", ""),
					huh.NewOption("ssh", "ssh"),
					huh.NewOption("pve", "pve"),
					huh.NewOption("hybrid", "hybrid"),
				).
				Value(&b.ExecMode),
		),
	).Run()
	if err == nil {
		backends := c.cfg.ProxmoxBackends()
		backends[idx] = b
		c.cfg.SetProxmoxBackends(backends)
	}
	return err
}

func (c proxmoxCRUD) Delete(idx int) error {
	if idx < 0 || idx >= len(c.cfg.ProxmoxBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	backends := c.cfg.ProxmoxBackends()
	c.cfg.SetProxmoxBackends(append(backends[:idx], backends[idx+1:]...))
	return nil
}
