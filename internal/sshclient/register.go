package sshclient

import "github.com/shareed2k/honey/internal/hostexec"

// DialHoneyHost connects to the remote host using SSH.
func DialHoneyHost(user, hostAlias string, overridePort int, identityFile string) (hostexec.HostClient, error) {
	return DialHoneyClient(user, hostAlias, overridePort, identityFile)
}
