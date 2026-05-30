package proxmoxprovider

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
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
		if url == "" {
			url = cliFlags.url
		}
		if user == "" {
			user = f.ProxmoxUser
		}
		if user == "" {
			user = cliFlags.user
		}
		if pass == "" {
			pass = f.ProxmoxPassword
		}
		if pass == "" {
			pass = cliFlags.password
		}
		if tid == "" {
			tid = f.ProxmoxTokenID
		}
		if tid == "" {
			tid = cliFlags.tokenID
		}
		if tsec == "" {
			tsec = f.ProxmoxTokenSecret
		}
		if tsec == "" {
			tsec = cliFlags.tokenSecret
		}
		if !insecure && f.ProxmoxInsecure {
			insecure = true
		}
		if !insecure && cliFlags.insecure {
			insecure = true
		}
		execMode := strings.ToLower(strings.TrimSpace(e.ExecMode))
		switch execMode {
		case "pve", "hybrid":
		default:
			execMode = "ssh"
		}
		b := searchrun.WithDockerDiscover(
			&Proxmox{
				Name:        e.Name,
				URL:         url,
				User:        user,
				Password:    pass,
				TokenID:     tid,
				TokenSecret: tsec,
				Insecure:    insecure,
				ExecMode:    execMode,
			},
			searchrun.MergeDockerDiscover(cfg.Defaults.DockerDiscover, e.DockerDiscover),
		)
		out = append(out, b)
	}
	return out
}

func (proxmoxFactory) Default(f searchrun.ProviderFlags) hosts.Backend {
	url := f.ProxmoxURL
	if url == "" {
		url = cliFlags.url
	}
	user := f.ProxmoxUser
	if user == "" {
		user = cliFlags.user
	}
	pass := f.ProxmoxPassword
	if pass == "" {
		pass = cliFlags.password
	}
	tid := f.ProxmoxTokenID
	if tid == "" {
		tid = cliFlags.tokenID
	}
	tsec := f.ProxmoxTokenSecret
	if tsec == "" {
		tsec = cliFlags.tokenSecret
	}
	insecure := f.ProxmoxInsecure || cliFlags.insecure
	return searchrun.WithDockerDiscover(
		&Proxmox{
			URL:         url,
			User:        user,
			Password:    pass,
			TokenID:     tid,
			TokenSecret: tsec,
			Insecure:    insecure,
		},
		config.DockerDiscover{},
	)
}

func (proxmoxFactory) BackendRows(cfg *config.File) []config.BackendRow {
	rows := make([]config.BackendRow, 0, len(cfg.Backends.Proxmox))
	for _, e := range cfg.Backends.Proxmox {
		rows = append(rows, config.BackendRow{Kind: "proxmox", Name: e.Name, Hint: strings.TrimSpace(e.URL)})
	}
	return rows
}

func (proxmoxFactory) BackendKind() string { return "proxmox" }

func (proxmoxFactory) BackendSlicePtr(cfg *config.File) any { return &cfg.Backends.Proxmox }

func (proxmoxFactory) RegisterFlags(cmd *cobra.Command) { RegisterFlags(cmd) }

func (proxmoxFactory) ProviderName() string { return "proxmox" }

func (proxmoxFactory) ExecutorFor(r hosts.Record) hostexec.Executor {
	return resolveProxmoxExecutor(r)
}

func (proxmoxFactory) ReconfigureFromConfig(cfg *config.File) { reconfigureProxmox(cfg) }
