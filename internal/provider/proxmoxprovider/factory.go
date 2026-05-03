package proxmoxprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func init() {
	searchrun.Register(proxmoxFactory{})
}

type proxmoxFactory struct{}

func (proxmoxFactory) FromConfig(cfg *config.File, f searchrun.ProviderFlags) []hosts.Backend {
	out := make([]hosts.Backend, 0, len(cfg.Backends.Proxmox))
	for _, e := range cfg.Backends.Proxmox {
		url, user, pass, tid, tsec, insecure := e.URL, e.User, e.Password, e.TokenID, e.TokenSecret, e.Insecure
		if url == "" {
			url = f.ProxmoxURL
		}
		if user == "" {
			user = f.ProxmoxUser
		}
		if pass == "" {
			pass = f.ProxmoxPassword
		}
		if tid == "" {
			tid = f.ProxmoxTokenID
		}
		if tsec == "" {
			tsec = f.ProxmoxTokenSecret
		}
		if !insecure && f.ProxmoxInsecure {
			insecure = true
		}
		out = append(out, &Proxmox{
			Name:        e.Name,
			URL:         url,
			User:        user,
			Password:    pass,
			TokenID:     tid,
			TokenSecret: tsec,
			Insecure:    insecure,
		})
	}
	return out
}

func (proxmoxFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	return &Proxmox{
		URL:         f.ProxmoxURL,
		User:        f.ProxmoxUser,
		Password:    f.ProxmoxPassword,
		TokenID:     f.ProxmoxTokenID,
		TokenSecret: f.ProxmoxTokenSecret,
		Insecure:    f.ProxmoxInsecure,
	}
}

func (proxmoxFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Proxmox))
	for _, e := range cfg.Backends.Proxmox {
		rows = append(rows, config.BackendRow{Kind: "proxmox", Name: e.Name, Hint: strings.TrimSpace(e.URL)})
	}
	return rows
}
