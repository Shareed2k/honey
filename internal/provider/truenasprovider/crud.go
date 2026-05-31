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
	var insecure, inclAppliance, inclVMs, inclVirt bool
	inclAppliance, inclVMs, inclVirt = true, true, true
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewInput().Title("URL (https://truenas.example.com)").Value(&url),
			huh.NewInput().Title("Username (API key owner, default root)").Value(&user),
			huh.NewInput().Title("API key").EchoMode(huh.EchoModePassword).Value(&apiKey),
			huh.NewInput().Title("SSH user for appliance (optional)").Value(&sshUser),
			huh.NewConfirm().Title("Insecure (skip TLS verify)?").Value(&insecure),
			huh.NewConfirm().Title("List appliance?").Value(&inclAppliance),
			huh.NewConfirm().Title("List KVM VMs?").Value(&inclVMs),
			huh.NewConfirm().Title("List virt instances?").Value(&inclVirt),
		),
	).Run()
	if err == nil {
		cfg.Backends.TrueNAS = append(cfg.Backends.TrueNAS, config.TrueNASBackend{
			Name:             name,
			URL:              url,
			Username:         user,
			APIKey:           apiKey,
			SSHUser:          sshUser,
			Insecure:         insecure,
			IncludeAppliance: &inclAppliance,
			IncludeVMs:       &inclVMs,
			IncludeVirt:      &inclVirt,
		})
	}
	return err
}

func (truenasCRUD) Edit(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.TrueNAS) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.TrueNAS[idx]
	// Dereference *bool fields with defaults for the form.
	inclAppliance := b.IncludeAppliance == nil || *b.IncludeAppliance
	inclVMs := b.IncludeVMs == nil || *b.IncludeVMs
	inclVirt := b.IncludeVirt == nil || *b.IncludeVirt
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewInput().Title("URL").Value(&b.URL),
			huh.NewInput().Title("Username").Value(&b.Username),
			huh.NewInput().Title("API key").EchoMode(huh.EchoModePassword).Value(&b.APIKey),
			huh.NewInput().Title("SSH user for appliance (optional)").Value(&b.SSHUser),
			huh.NewConfirm().Title("Insecure (skip TLS verify)?").Value(&b.Insecure),
			huh.NewConfirm().Title("List appliance?").Value(&inclAppliance),
			huh.NewConfirm().Title("List KVM VMs?").Value(&inclVMs),
			huh.NewConfirm().Title("List virt instances?").Value(&inclVirt),
		),
	).Run()
	if err == nil {
		b.IncludeAppliance = &inclAppliance
		b.IncludeVMs = &inclVMs
		b.IncludeVirt = &inclVirt
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
