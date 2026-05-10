package sshclient

import "github.com/shareed2k/honey/internal/hostexec"

func init() {
	hostexec.SetDialHoney(func(user, hostAlias string) (hostexec.HostClient, error) {
		return DialHoneyClient(user, hostAlias)
	})
	hostexec.SetSSHRunTunnel(RunTunnelGo)
}
