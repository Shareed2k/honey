package dockerprovider

import (
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

func resolveViaLocal(name string, locals []config.LocalBackend, defaultUser string) (SSHHop, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SSHHop{}, false, nil
	}
	if len(locals) == 0 {
		return SSHHop{}, false, fmt.Errorf(
			"local backend %q not found: no backends.local defined in config",
			name,
		)
	}

	backendName := name
	hostName := ""
	if i := strings.Index(name, "/"); i >= 0 {
		backendName = strings.TrimSpace(name[:i])
		hostName = strings.TrimSpace(name[i+1:])
	}

	for _, lb := range locals {
		if strings.TrimSpace(lb.Name) != backendName {
			continue
		}
		h, ok, err := pickLocalHost(lb, hostName)
		if err != nil {
			return SSHHop{}, false, err
		}
		if !ok {
			return SSHHop{}, false, fmt.Errorf("local backend %q has no hosts", backendName)
		}
		return hopFromLocalHost(h, defaultUser)
	}

	if !strings.Contains(name, "/") {
		for _, lb := range locals {
			for _, h := range lb.Hosts {
				if strings.TrimSpace(h.Name) == name {
					return hopFromLocalHost(h, defaultUser)
				}
			}
		}
	}

	return SSHHop{}, false, fmt.Errorf("local backend %q not found", name)
}

func pickLocalHost(lb config.LocalBackend, hostName string) (config.LocalHost, bool, error) {
	if strings.TrimSpace(hostName) == "" {
		if len(lb.Hosts) == 0 {
			return config.LocalHost{}, false, nil
		}
		return lb.Hosts[0], true, nil
	}
	for _, h := range lb.Hosts {
		if strings.TrimSpace(h.Name) == hostName {
			return h, true, nil
		}
	}
	return config.LocalHost{}, false, fmt.Errorf("local backend %q has no host %q", lb.Name, hostName)
}

func hopFromLocalHost(h config.LocalHost, defaultUser string) (SSHHop, bool, error) {
	hop := SSHHop{
		Host: strings.TrimSpace(h.PrimaryIP),
		User: strings.TrimSpace(defaultUser),
	}
	if hop.Host == "" {
		hop.Host = strings.TrimSpace(h.Name)
	}
	rec := hosts.Record{Meta: h.Meta}
	if p, ok := hosts.MetaSSHPort(&rec); ok {
		hop.Port = p
	}
	if id, ok := hosts.MetaSSHIdentityFile(&rec); ok {
		hop.IdentityFile = id
	}
	if hop.Host == "" {
		return SSHHop{}, false, fmt.Errorf("local host %q has no primary_ip", h.Name)
	}
	return hop, true, nil
}
