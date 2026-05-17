package sshclient

import "github.com/shareed2k/honey/internal/hostexec"

func init() {
	hostexec.SetDialHoney(func(user, hostAlias string, overridePort int, identityFile string) (hostexec.HostClient, error) {
		return DialHoneyClient(user, hostAlias, overridePort, identityFile)
	})
	hostexec.SetSSHRunTunnel(RunTunnelGo)
}
