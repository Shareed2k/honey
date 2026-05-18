package dockerprovider

import (
	"context"
	"strings"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/shareed2k/honey/internal/hosts"
)

const discoverMaxConcurrent = 8

// DiscoverOnVMs lists Docker containers on cloud VM records from an earlier search pass.
// VMs are already scoped by --backends / --provider filters on the parent search.
func DiscoverOnVMs(ctx context.Context, q hosts.Query, vms []hosts.Record) ([]hosts.Record, error) {
	if !FeatureDockerViaProviders() {
		zap.L().Warn("docker auto-discover skipped: set HONEY_FEATURE_DOCKER_VIA_PROVIDERS=1")
		return nil, nil
	}
	if len(vms) == 0 {
		return nil, nil
	}

	sem := make(chan struct{}, discoverMaxConcurrent)
	g, ctx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	var merged []hosts.Record

	for _, vm := range vms {
		if strings.TrimSpace(vm.PrimaryIP) == "" {
			continue
		}
		vm := vm
		g.Go(func() error {
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-sem }()

			recs, err := searchVMContainers(ctx, vm, q)
			if err != nil {
				zap.L().Warn("docker discover failed", zap.String("vm", vm.Name), zap.String("provider", vm.Provider), zap.Error(err))
				return nil
			}
			mu.Lock()
			merged = append(merged, recs...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return merged, nil
}
