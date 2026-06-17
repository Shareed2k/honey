// Package localprovider provides the ability to manage and search manually defined hosts.
package localprovider

import (
	"fmt"

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

func (localCRUD) ListOptions() []huh.Option[string] {
	cfg := config.Get()
	opts := make([]huh.Option[string], 0, len(cfg.Backends.Local))
	for i, b := range cfg.Backends.Local {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Local: %s (%d hosts)", b.Name, len(b.Hosts)), fmt.Sprintf("local:%d", i)))
	}
	return opts
}

func (localCRUD) Add() error {
	cfg := config.Get()
	var name string
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Backend name").Value(&name),
		),
	).Run(); err != nil {
		return err
	}

	var hosts []config.LocalHost
	for {
		var hostName, primaryIP, sshUser string
		var addAnother bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title("Host name").Value(&hostName),
				huh.NewInput().Title("Primary IP").Value(&primaryIP),
				huh.NewInput().Title("SSH user for host (optional, leave blank for default)").Value(&sshUser),
				huh.NewConfirm().Title("Add another host?").Value(&addAnother),
			),
		).Run(); err != nil {
			return err
		}
		hosts = append(hosts, config.LocalHost{
			Name:      hostName,
			PrimaryIP: primaryIP,
			SSHUser:   sshUser,
		})
		if !addAnother {
			break
		}
	}

	cfg.Backends.Local = append(cfg.Backends.Local, config.LocalBackend{
		Name:  name,
		Hosts: hosts,
	})
	return nil
}

func (localCRUD) Edit(idx int) error {
	cfg := config.Get()
	if idx < 0 || idx >= len(cfg.Backends.Local) {
		return fmt.Errorf("index out of bounds")
	}
	b := cfg.Backends.Local[idx]

	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Backend name").Value(&b.Name),
		),
	).Run(); err != nil {
		return err
	}

	// Edit each existing host in turn.
	for i := range b.Hosts {
		h := &b.Hosts[i]
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title(fmt.Sprintf("Host %d — name", i+1)).Value(&h.Name),
				huh.NewInput().Title(fmt.Sprintf("Host %d — primary IP", i+1)).Value(&h.PrimaryIP),
				huh.NewInput().Title(fmt.Sprintf("Host %d — SSH user (optional)", i+1)).Value(&h.SSHUser),
				huh.NewInput().Title(fmt.Sprintf("Host %d — zone (optional)", i+1)).Value(&h.Zone),
				huh.NewInput().Title(fmt.Sprintf("Host %d — region (optional)", i+1)).Value(&h.Region),
			),
		).Run(); err != nil {
			return err
		}
	}

	// Offer to append new hosts.
	var addMore bool
	_ = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().Title("Add more hosts?").Value(&addMore),
		),
	).Run()
	for addMore {
		var hostName, primaryIP, sshUser string
		var again bool
		if err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title("Host name").Value(&hostName),
				huh.NewInput().Title("Primary IP").Value(&primaryIP),
				huh.NewInput().Title("SSH user (optional)").Value(&sshUser),
				huh.NewConfirm().Title("Add another host?").Value(&again),
			),
		).Run(); err != nil {
			return err
		}
		b.Hosts = append(b.Hosts, config.LocalHost{
			Name:      hostName,
			PrimaryIP: primaryIP,
			SSHUser:   sshUser,
		})
		addMore = again
	}

	cfg.Backends.Local[idx] = b
	return nil
}

func (localCRUD) Delete(idx int) error {
	cfg := config.Get()
	if idx < 0 || idx >= len(cfg.Backends.Local) {
		return fmt.Errorf("index out of bounds")
	}
	cfg.Backends.Local = append(cfg.Backends.Local[:idx], cfg.Backends.Local[idx+1:]...)
	return nil
}
