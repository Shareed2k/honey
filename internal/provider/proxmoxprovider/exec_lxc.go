package proxmoxprovider

import (
	"strings"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

// dialPVELXC connects to the LXC guest over SSH for commands and file operations.
// Proxmox VE does not implement HTTP POST .../lxc/{vmid}/exec on the cluster API (501);
// the web UI shell uses termproxy/vncwebsocket instead, which honey handles in internal/webserver.
func dialPVELXC(_ hostexec.ProxmoxBackendRuntime, user string, r hosts.Record) (hostexec.HostClient, error) {
	if err := requireLXCMeta(r); err != nil {
		return nil, err
	}
	return dialSSHToLXCGuest(user, r)
}

// dialHybridLXC uses guest SSH for both commands and SFTP. The historical split (PVE exec + SSH
// files) relied on a non-standard REST exec endpoint; upstream Proxmox only lists console-related
// LXC HTTP APIs, not command execution.
func dialHybridLXC(_ hostexec.ProxmoxBackendRuntime, user string, r hosts.Record) (hostexec.HostClient, error) {
	if err := requireLXCMeta(r); err != nil {
		return nil, err
	}
	return dialSSHToLXCGuest(user, r)
}

func requireLXCMeta(r hosts.Record) error {
	if strings.TrimSpace(r.Meta["node"]) == "" || strings.TrimSpace(r.Meta["vmid"]) == "" {
		return errProxmoxMeta
	}
	return nil
}

func dialSSHToLXCGuest(user string, r hosts.Record) (hostexec.HostClient, error) {
	ip := strings.TrimSpace(r.PrimaryIP)
	if ip == "" {
		return nil, errProxmoxLXCNoGuestIP
	}
	return sshclient.DialHoneyClient(user, ip, 0)
}
