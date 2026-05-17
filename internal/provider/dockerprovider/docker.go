package dockerprovider

import (
	"context"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

// Docker implements hosts.Backend for Engine API search (local, Moby ssh://, or Honey SSH).
type Docker struct {
	Config BackendConfig
}

// ID returns the honey backend identifier ("docker").
func (d *Docker) ID() string { return "docker" }

// BackendName returns the optional YAML backends.docker[].name value.
func (d *Docker) BackendName() string { return strings.TrimSpace(d.Config.Name) }

// CacheIdentity scopes cache entries per docker backend configuration.
func (d *Docker) CacheIdentity() string {
	c := d.Config
	return strings.Join([]string{
		strings.TrimSpace(c.Name),
		c.ResolvedHost(),
		strings.TrimSpace(c.ViaLocal),
		strings.TrimSpace(c.ViaSSH.Host),
		strings.TrimSpace(c.Socket),
	}, "\x1e")
}

var _ hosts.Backend = (*Docker)(nil)

// Search lists containers and/or swarm tasks from the configured Engine endpoint.
func (d *Docker) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	bc := d.Config
	if s := strings.TrimSpace(q.DockerHost); s != "" {
		bc.Host = s
	}
	if s := strings.TrimSpace(q.DockerViaLocal); s != "" {
		bc.ViaLocal = s
	}
	if s := strings.TrimSpace(q.DockerViaSSHHost); s != "" {
		bc.ViaSSH.Host = s
	}
	if s := strings.TrimSpace(q.DockerSocket); s != "" {
		bc.Socket = s
	}
	if s := strings.TrimSpace(q.DockerPlatform); s != "" {
		bc.Platform = s
	}
	if q.DockerAllContainers {
		bc.AllContainers = true
	}
	if s := strings.TrimSpace(q.DockerMode); s != "" {
		bc.Mode = s
	}
	if strings.TrimSpace(q.DockerSSHUser) != "" {
		bc.SSHUser = strings.TrimSpace(q.DockerSSHUser)
	}
	opts := APIClientOptions{SSHUser: q.DockerSSHUser}
	return searchBackend(ctx, bc, q, opts)
}
