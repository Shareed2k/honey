package proxmoxprovider

import (
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.RegisterCRUD(proxmoxCRUD{})
}

type proxmoxCRUD struct{}

func (proxmoxCRUD) ID() string   { return "proxmox" }
func (proxmoxCRUD) Name() string { return "Proxmox" }

func (proxmoxCRUD) ListOptions(cfg *config.File) []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(cfg.Backends.Proxmox))
	for i, b := range cfg.Backends.Proxmox {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Proxmox: %s (%s)", b.Name, b.URL), fmt.Sprintf("proxmox:%d", i)))
	}
	return opts
}

func (proxmoxCRUD) Add(cfg *config.File) error {
	var name, url, user, password, tokenID, tokenSecret string
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
		),
	).Run()
	if err == nil {
		cfg.Backends.Proxmox = append(cfg.Backends.Proxmox, config.ProxmoxBackend{
			Name:        name,
			URL:         url,
			User:        user,
			Password:    password,
			TokenID:     tokenID,
			TokenSecret: tokenSecret,
			Insecure:    insecure,
		})
	}
	return err
}

func (proxmoxCRUD) Edit(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.Proxmox) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.Proxmox[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("URL (e.g. https://10.0.0.10:8006/api2/json)").Value(&b.URL),
			huh.NewInput().Title("User (optional)").Value(&b.User),
			huh.NewInput().Title("Password (optional)").EchoMode(huh.EchoModePassword).Value(&b.Password),
			huh.NewInput().Title("Token ID (optional)").Value(&b.TokenID),
			huh.NewInput().Title("Token Secret (optional)").EchoMode(huh.EchoModePassword).Value(&b.TokenSecret),
			huh.NewConfirm().Title("Insecure (Skip TLS Verify)?").Value(&b.Insecure),
		),
	).Run()
	if err == nil {
		cfg.Backends.Proxmox[idx] = b
	}
	return err
}

func (proxmoxCRUD) Delete(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.Proxmox) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.Proxmox = append(cfg.Backends.Proxmox[:idx], cfg.Backends.Proxmox[idx+1:]...)
	return nil
}
