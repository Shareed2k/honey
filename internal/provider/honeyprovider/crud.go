package honeyprovider

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
)

type honeyCRUD struct {
	cfg ConfigProvider
}

func (c honeyCRUD) ID() string   { return "honey" }
func (c honeyCRUD) Name() string { return "Honey" }

func (c honeyCRUD) ListOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.cfg.HoneyBackends()))
	for i, b := range c.cfg.HoneyBackends() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Honey: %s (%s)", b.Name, b.URL), fmt.Sprintf("honey:%d", i)))
	}
	return opts
}

func (c honeyCRUD) Add() error {
	var name, url, token string
	var insecure bool

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Backend name").Value(&name),
			huh.NewInput().Title("Honey server URL").Value(&url),
			huh.NewInput().Title("Auth token (optional)").Value(&token),
			huh.NewConfirm().Title("Insecure TLS?").Value(&insecure),
		),
	).Run(); err != nil {
		return err
	}

	c.cfg.SetHoneyBackends(append(c.cfg.HoneyBackends(), config.HoneyBackend{
		Name:     name,
		URL:      url,
		Token:    token,
		Insecure: insecure,
	}))
	return nil
}

func (c honeyCRUD) Edit(idx int) error {
	if idx < 0 || idx >= len(c.cfg.HoneyBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	b := c.cfg.HoneyBackends()[idx]

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Backend name").Value(&b.Name),
			huh.NewInput().Title("Honey server URL").Value(&b.URL),
			huh.NewInput().Title("Auth token (optional)").Value(&b.Token),
			huh.NewConfirm().Title("Insecure TLS?").Value(&b.Insecure),
		),
	).Run(); err != nil {
		return err
	}

	backends := c.cfg.HoneyBackends()
	backends[idx] = b
	c.cfg.SetHoneyBackends(backends)
	return nil
}

func (c honeyCRUD) Delete(idx int) error {
	if idx < 0 || idx >= len(c.cfg.HoneyBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	backends := c.cfg.HoneyBackends()
	c.cfg.SetHoneyBackends(append(backends[:idx], backends[idx+1:]...))
	return nil
}
