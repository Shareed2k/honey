package dockerprovider

import (
	"fmt"
	"strings"
)

// NormalizePlatform returns linux or windows.
func NormalizePlatform(p string) string {
	if strings.EqualFold(strings.TrimSpace(p), "windows") {
		return "windows"
	}
	return "linux"
}

// DefaultSocket returns the default Engine socket path/URI for a platform.
func DefaultSocket(platform string) string {
	if NormalizePlatform(platform) == "windows" {
		return "npipe:////./pipe/docker_engine"
	}
	return "/var/run/docker.sock"
}

// ResolveSocket returns configured socket or platform default.
func ResolveSocket(socket, platform string) string {
	s := strings.TrimSpace(socket)
	if s != "" {
		return s
	}
	return DefaultSocket(platform)
}

// DialParams describes how to dial the remote Engine socket over SSH.
type DialParams struct {
	Network string
	Address string
	HostURL string
}

// SocketDialParams maps a socket URI and platform to SSH dial network/address and Moby host URL.
func SocketDialParams(socket, platform string) (DialParams, error) {
	socket = ResolveSocket(socket, platform)

	switch {
	case strings.HasPrefix(socket, "unix://"):
		path := strings.TrimPrefix(socket, "unix://")
		return DialParams{Network: "unix", Address: path, HostURL: "unix://" + path}, nil
	case strings.HasPrefix(socket, "npipe://"):
		return DialParams{Network: "tcp", Address: "127.0.0.1:2375", HostURL: "tcp://127.0.0.1:2375"}, nil
	case strings.HasPrefix(socket, "tcp://"):
		addr := strings.TrimPrefix(socket, "tcp://")
		return DialParams{Network: "tcp", Address: addr, HostURL: socket}, nil
	case strings.HasPrefix(socket, "/"):
		return DialParams{Network: "unix", Address: socket, HostURL: "unix://" + socket}, nil
	default:
		return DialParams{}, fmt.Errorf("unsupported docker socket %q", socket)
	}
}
