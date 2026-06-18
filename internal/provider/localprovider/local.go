package localprovider

import (
	"context"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// Local implements static host search.
type Local struct {
	Name  string // optional config label
	Hosts []config.LocalHost
}

// ID returns the honey backend identifier ("local").
func (Local) ID() string { return "local" }

// BackendName returns the optional YAML backends.local[].name value.
func (l *Local) BackendName() string { return strings.TrimSpace(l.Name) }

// CacheIdentity scopes cache entries per backend name.
func (l *Local) CacheIdentity() string {
	return strings.TrimSpace(l.Name)
}

var _ hosts.Backend = (*Local)(nil)

// Search returns local static instances matching the query.
func (l *Local) Search(_ context.Context, q hosts.Query) ([]hosts.Record, error) {
	var out []hosts.Record

	for _, h := range l.Hosts {
		ok, err := q.MatchesName(h.Name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}

		// Ensure meta is instantiated and copied so it's not mutated across searches
		meta := make(map[string]string)
		for k, v := range h.Meta {
			meta[k] = v
		}
		if h.SSHUser != "" {
			meta["ssh_user"] = h.SSHUser
		}

		out = append(out, hosts.Record{
			Provider:  "local",
			Name:      h.Name,
			PrimaryIP: h.PrimaryIP,
			ExtraIPs:  h.ExtraIPs,
			Zone:      h.Zone,
			Region:    h.Region,
			Meta:      meta,
		})
	}
	return out, nil
}
