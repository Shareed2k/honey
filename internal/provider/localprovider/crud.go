package localprovider

import (
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.RegisterCRUD(localCRUD{})
}

type localCRUD struct{}

func (localCRUD) ID() string   { return "local" }
func (localCRUD) Name() string { return "Local" }

func (localCRUD) ListOptions(cfg *config.File) []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(cfg.Backends.Local))
	for i, b := range cfg.Backends.Local {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Local: %s (%d hosts)", b.Name, len(b.Hosts)), fmt.Sprintf("local:%d", i)))
	}
	return opts
}

func (localCRUD) Add(cfg *config.File) error {
	var name, hostsStr string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&name),
			huh.NewText().Title("Hosts (comma-separated hostnames or IPs for a simple setup)").Value(&hostsStr),
		),
	).Run()
	if err == nil {
		var parsedHosts []config.LocalHost
		for _, h := range strings.Split(hostsStr, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				parsedHosts = append(parsedHosts, config.LocalHost{Name: h, PrimaryIP: h})
			}
		}
		cfg.Backends.Local = append(cfg.Backends.Local, config.LocalBackend{
			Name:  name,
			Hosts: parsedHosts,
		})
	}
	return err
}

func (localCRUD) Edit(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.Local) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.Local[idx]
	var hostsStr string
	var hNames []string
	for _, h := range b.Hosts {
		hNames = append(hNames, h.Name)
	}
	hostsStr = strings.Join(hNames, ", ")

	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Name").Value(&b.Name),
			huh.NewText().Title("Hosts (comma-separated hostnames/IPs)").Value(&hostsStr),
		),
	).Run()
	if err == nil {
		var parsedHosts []config.LocalHost
		for _, h := range strings.Split(hostsStr, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				// We try to retain existing IPs if the name hasn't changed. Simple implementation here.
				found := false
				for _, exist := range b.Hosts {
					if exist.Name == h {
						parsedHosts = append(parsedHosts, exist)
						found = true
						break
					}
				}
				if !found {
					parsedHosts = append(parsedHosts, config.LocalHost{Name: h, PrimaryIP: h})
				}
			}
		}
		b.Hosts = parsedHosts
		cfg.Backends.Local[idx] = b
	}
	return err
}

func (localCRUD) Delete(cfg *config.File, idx int) error {
	if idx < 0 || idx >= len(cfg.Backends.Local) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.Local = append(cfg.Backends.Local[:idx], cfg.Backends.Local[idx+1:]...)
	return nil
}
