package truenasprovider

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.RegisterCRUD(truenasCRUD{})
}

type truenasCRUD struct{}

func (truenasCRUD) ID() string   { return "truenas" }
func (truenasCRUD) Name() string { return "TrueNAS" }

func (truenasCRUD) ListOptions(cfg *config.File) []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(cfg.Backends.TrueNAS))
	for i, b := range cfg.Backends.TrueNAS {
		opts = append(opts, huh.NewOption(fmt.Sprintf("TrueNAS: %s (%s)", b.Name, b.URL), fmt.Sprintf("truenas:%d", i)))
	}
	return opts
}

func (truenasCRUD) Add(cfg *config.File) error {
	var name, url, user, apiKey, sshUser string
	var insecure bool
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("URL (https://truenas.example.com)").Value(&url),
			huh.NewInput().Title("Username (API key owner, default root)").Value(&user),
			huh.NewInput().Title("API key").EchoMode(huh.EchoModePassword).Value(&apiKey),
			huh.NewInput().Title("SSH user for appliance (optional)").Value(&sshUser),
			huh.NewConfirm().Title("Insecure (skip TLS verify)?").Value(&insecure),
		),
	).Run()
	if err == nil {
		cfg.Backends.TrueNAS = append(cfg.Backends.TrueNAS, config.TrueNASBackend{
			Name:     name,
			URL:      url,
			Username: user,
			APIKey:   apiKey,
			SSHUser:  sshUser,
			Insecure: insecure,
		})
	}
	return err
}

func (truenasCRUD) Edit(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.TrueNAS) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.TrueNAS[idx]
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("URL").Value(&b.URL),
			huh.NewInput().Title("Username").Value(&b.Username),
			huh.NewInput().Title("API key").EchoMode(huh.EchoModePassword).Value(&b.APIKey),
			huh.NewInput().Title("SSH user for appliance (optional)").Value(&b.SSHUser),
			huh.NewConfirm().Title("Insecure (skip TLS verify)?").Value(&b.Insecure),
		),
	).Run()
	if err == nil {
		cfg.Backends.TrueNAS[idx] = b
	}
	return err
}

func (truenasCRUD) Delete(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.TrueNAS) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.TrueNAS = append(cfg.Backends.TrueNAS[:idx], cfg.Backends.TrueNAS[idx+1:]...)
	return nil
}
