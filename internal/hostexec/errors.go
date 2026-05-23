package hostexec

import "errors"

var (
	errDialNotConfigured          = errors.New("hostexec: SSH dial not configured (import internal/sshclient)")
	errInteractiveNotConfigured   = errors.New("hostexec: SSH interactive not configured (wire ui)")
	errTunnelNotConfigured        = errors.New("hostexec: SSH tunnel not configured (import internal/sshclient)")
	errNoHostIP                   = errors.New("hostexec: no primary IP for host")
	errTrueNASTunnelNotConfigured = errors.New("hostexec: TrueNAS API tunnel not configured (import internal/ui)")
	errTrueNASTunnelOnly          = errors.New("hostexec: TrueNAS API-shell record supports port-forward (tunnel) only")
)
