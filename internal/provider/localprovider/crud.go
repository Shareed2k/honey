// Package localprovider provides the ability to manage and search manually defined hosts.
package localprovider

import (
	"fmt"

	"charm.land/huh/v2"
	"github.com/shareed2k/honey/internal/config"
)

type localCRUD struct {
	cfg ConfigProvider
}

func (c localCRUD) ID() string   { return "local" }
func (c localCRUD) Name() string { return "Local" }

func (c localCRUD) ListOptions() []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(c.cfg.LocalBackends()))
	for i, b := range c.cfg.LocalBackends() {
		opts = append(opts, huh.NewOption(fmt.Sprintf("Local: %s (%d hosts)", b.Name, len(b.Hosts)), fmt.Sprintf("local:%d", i)))
	}
	return opts
}

func (c localCRUD) Add() error {
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

	c.cfg.SetLocalBackends(append(c.cfg.LocalBackends(), config.LocalBackend{
		Name:  name,
		Hosts: hosts,
	}))
	return nil
}

func (c localCRUD) Edit(idx int) error {
	if idx < 0 || idx >= len(c.cfg.LocalBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	b := c.cfg.LocalBackends()[idx]

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

	backends := c.cfg.LocalBackends()
	backends[idx] = b
	c.cfg.SetLocalBackends(backends)
	return nil
}

func (c localCRUD) Delete(idx int) error {
	if idx < 0 || idx >= len(c.cfg.LocalBackends()) {
		return fmt.Errorf("index out of bounds")
	}
	backends := c.cfg.LocalBackends()
	c.cfg.SetLocalBackends(append(backends[:idx], backends[idx+1:]...))
	return nil
}
