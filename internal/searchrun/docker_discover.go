package searchrun

import (
	"context"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// DockerDiscoverFunc lists containers on cloud VM records from a completed search pass.
type DockerDiscoverFunc func(ctx context.Context, q hosts.Query, vms []hosts.Record) ([]hosts.Record, error)

var dockerDiscover DockerDiscoverFunc

// RegisterDockerDiscover registers the docker auto-discover hook from dockerprovider.
//
// This stays an init-registered extension point (like RegisterCRUD) rather than a
// constructor-injected dependency: the hook is consumed by dockerDiscoverWrapper
// instances created across many unrelated provider factories (aws, gcp, proxmox,
// local, consul) that must not import dockerprovider. Registration is the idiomatic
// Go pattern for that one-to-many optional extension wiring.
func RegisterDockerDiscover(fn DockerDiscoverFunc) {
	dockerDiscover = fn
}

type dockerDiscoverWrapper struct {
	hosts.Backend
	discover config.DockerDiscover
}

func (w *dockerDiscoverWrapper) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	recs, err := w.Backend.Search(ctx, q)
	if err != nil || len(recs) == 0 {
		return recs, err
	}
	if !w.discover.Enabled {
		return recs, nil
	}
	for i := range recs {
		if recs[i].Meta == nil {
			recs[i].Meta = make(map[string]string)
		}
		recs[i].Meta["docker_discover_enabled"] = "1"
		if w.discover.RunAs != "" {
			recs[i].Meta["docker_discover_run_as"] = w.discover.RunAs
		}
		if w.discover.Socket != "" {
			recs[i].Meta["docker_discover_socket"] = w.discover.Socket
		}
		if w.discover.Platform != "" {
			recs[i].Meta["docker_discover_platform"] = w.discover.Platform
		}
	}

	if dockerDiscover != nil {
		zap.L().Debug("docker discover starting for backend", zap.String("backend", w.ID()), zap.Int("vm_count", len(recs)))
		dockerRecs, derr := dockerDiscover(ctx, q, recs)
		if derr != nil {
			return nil, derr
		}
		if len(dockerRecs) > 0 {
			recs = append(recs, dockerRecs...)
		}
	}

	return recs, nil
}

// WithDockerDiscover wraps a backend to inject Docker auto-discover metadata into its records.
func WithDockerDiscover(b hosts.Backend, d config.DockerDiscover) hosts.Backend {
	return &dockerDiscoverWrapper{Backend: b, discover: d}
}

// MergeDockerDiscover merges a backend-specific discover config over defaults.
func MergeDockerDiscover(defaults, override config.DockerDiscover) config.DockerDiscover {
	out := defaults
	if override.Enabled {
		out.Enabled = true
	}
	if override.RunAs != "" {
		out.RunAs = override.RunAs
	}
	if override.Socket != "" {
		out.Socket = override.Socket
	}
	if override.Platform != "" {
		out.Platform = override.Platform
	}
	return out
}
