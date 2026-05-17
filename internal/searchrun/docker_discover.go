package searchrun

import (
	"context"
	"slices"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hosts"
)

// DockerDiscoverFunc lists containers on cloud VM records from a completed search pass.
type DockerDiscoverFunc func(ctx context.Context, q hosts.Query, vms []hosts.Record) ([]hosts.Record, error)

var dockerDiscover DockerDiscoverFunc

// RegisterDockerDiscover registers the docker auto-discover hook (from dockerprovider init).
func RegisterDockerDiscover(fn DockerDiscoverFunc) {
	dockerDiscover = fn
}

// appendDockerDiscover runs a second pass over VM records from the first search when
// --docker-discover-providers is set. VM records are already limited by --backends and
// --provider filters applied before the initial RunParallel.
func appendDockerDiscover(ctx context.Context, q hosts.Query, records []hosts.Record) ([]hosts.Record, error) {
	if len(q.DockerDiscoverProviders) == 0 || dockerDiscover == nil {
		return records, nil
	}
	want := make(map[string]struct{}, len(q.DockerDiscoverProviders))
	for _, p := range q.DockerDiscoverProviders {
		want[p] = struct{}{}
	}
	var vms []hosts.Record
	for _, r := range records {
		if _, ok := want[r.Provider]; ok {
			vms = append(vms, r)
		}
	}
	if len(vms) == 0 {
		zap.L().Debug("docker discover: no VM records from selected providers in this search")
		return records, nil
	}
	zap.L().Debug("docker discover starting", zap.Int("vm_count", len(vms)), zap.Strings("providers", q.DockerDiscoverProviders))
	dockerRecs, err := dockerDiscover(ctx, q, vms)
	if err != nil {
		return nil, err
	}
	if len(dockerRecs) == 0 {
		return records, nil
	}
	return hosts.MergeDedupe(records, dockerRecs), nil
}

// discoverProvidersIncluded returns true when every provider id in discover is also
// listed in q.Providers (when that list is non-empty).
func discoverProvidersIncluded(q hosts.Query) bool {
	if len(q.Providers) == 0 || len(q.DockerDiscoverProviders) == 0 {
		return true
	}
	for _, p := range q.DockerDiscoverProviders {
		if !slices.Contains(q.Providers, p) {
			return false
		}
	}
	return true
}
