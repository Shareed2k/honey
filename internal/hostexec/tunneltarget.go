package hostexec

import (
	"fmt"
	"net"
	"strings"
)

// TunnelScheme is the transport a mesh tunnel target names.
type TunnelScheme int

const (
	// TunnelTCP is a "host:port" target dialed over tcp.
	TunnelTCP TunnelScheme = iota
	// TunnelUnix is a "unix:<abs-path>" target dialed over a unix socket
	// (OpenSSH direct-streamlocal on the SSH path).
	TunnelUnix
)

// ParsedTarget is the decoded form of a mesh tunnel target string.
type ParsedTarget struct {
	Scheme TunnelScheme
	Socket string // set when Scheme==TunnelUnix
	Host   string // set when Scheme==TunnelTCP
	Port   string // set when Scheme==TunnelTCP
	Dest   string // raw destination (socket path or host:port), for gating/logs
}

const unixTargetPrefix = "unix:"

// FormatUnixTarget encodes a remote unix socket path as a mesh tunnel target.
func FormatUnixTarget(socketPath string) string { return unixTargetPrefix + socketPath }

// ParseTunnelTarget decodes a mesh tunnel target. A "unix:<abs-path>" string is
// a unix socket; anything else is a tcp "host:port". This is the single owner
// of the target wire format — the producer (honeyprovider) and the consumers
// (DialUpstream, the server-side tunnel gate) route through it, so the
// tcp/unix union lives in exactly one place.
func ParseTunnelTarget(target string) (ParsedTarget, error) {
	if sock, ok := strings.CutPrefix(target, unixTargetPrefix); ok {
		if !strings.HasPrefix(sock, "/") {
			return ParsedTarget{}, fmt.Errorf("unix tunnel target must be absolute: %q", sock)
		}
		return ParsedTarget{Scheme: TunnelUnix, Socket: sock, Dest: sock}, nil
	}
	host, port, _ := net.SplitHostPort(target)
	return ParsedTarget{Scheme: TunnelTCP, Host: host, Port: port, Dest: target}, nil
}
