package dockerprovider

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

// SSHHop is a resolved SSH target for Honey SSH transport to Docker.
type SSHHop struct {
	Host         string
	Port         int
	User         string
	IdentityFile string
}

// RecordHostURI returns a stable label stored on docker records.
func (h SSHHop) RecordHostURI() string {
	user := strings.TrimSpace(h.User)
	host := strings.TrimSpace(h.Host)
	if user != "" {
		return "honey-ssh://" + user + "@" + host
	}
	return "honey-ssh://" + host
}

// HopRecord builds a hosts.Record used for ClientCache key lookup.
func (h SSHHop) HopRecord() hosts.Record {
	meta := map[string]string{}
	if h.Port > 0 {
		meta["ssh_port"] = fmt.Sprintf("%d", h.Port)
	}
	if id := strings.TrimSpace(h.IdentityFile); id != "" {
		meta["ssh_identity_file"] = id
	}
	return hosts.Record{
		Provider:  "local",
		Name:      h.Host,
		PrimaryIP: h.Host,
		Meta:      meta,
	}
}

// ResolveSSHHop resolves SSH settings from backend config, optional local backends, or a VM record.
func ResolveSSHHop(bc BackendConfig, vm *hosts.Record) (SSHHop, bool, error) {
	if vm != nil && strings.TrimSpace(vm.PrimaryIP) != "" {
		hop := SSHHop{Host: strings.TrimSpace(vm.PrimaryIP), User: strings.TrimSpace(bc.SSHUser)}
		if p, ok := hosts.MetaSSHPort(vm); ok {
			hop.Port = p
		}
		if id, ok := hosts.MetaSSHIdentityFile(vm); ok {
			hop.IdentityFile = id
		}
		if u := strings.TrimSpace(vm.Meta["ssh_user"]); u != "" {
			hop.User = u
		}
		return hop, true, nil
	}
	if strings.TrimSpace(bc.ViaSSH.Host) != "" {
		return SSHHop{
			Host:         strings.TrimSpace(bc.ViaSSH.Host),
			Port:         bc.ViaSSH.Port,
			User:         strings.TrimSpace(bc.ViaSSH.User),
			IdentityFile: strings.TrimSpace(bc.ViaSSH.IdentityFile),
		}, true, nil
	}
	if name := strings.TrimSpace(bc.ViaLocal); name != "" {
		return resolveViaLocal(name, localBackendsForResolve(bc), bc.SSHUser)
	}
	return SSHHop{}, false, nil
}

// DialSSH opens an SSH client using Honey's sshclient stack.
func DialSSH(hop SSHHop, defaultUser string) (*ssh.Client, func(), error) {
	user := strings.TrimSpace(hop.User)
	if user == "" {
		user = strings.TrimSpace(defaultUser)
	}
	return sshclient.DialSSHClient(user, hop.Host, hop.Port, hop.IdentityFile)
}

func localBackendsForResolve(bc BackendConfig) []config.LocalBackend {
	return bc.LocalBackends
}
