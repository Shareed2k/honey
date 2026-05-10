package hostexec

import "errors"

var (
	errDialNotConfigured        = errors.New("hostexec: SSH dial not configured (import internal/sshclient)")
	errInteractiveNotConfigured = errors.New("hostexec: SSH interactive not configured (wire ui)")
	errTunnelNotConfigured      = errors.New("hostexec: SSH tunnel not configured (import internal/sshclient)")
	errNoHostIP                 = errors.New("hostexec: no primary IP for host")
)
