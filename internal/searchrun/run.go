package searchrun

import (
	"context"
	"path/filepath"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
)

// RunSearch executes hosts.RunParallel with an on-disk cache under cacheDir.
// If cacheDir is empty, hosts.DefaultCacheDir is used.
func RunSearch(
	ctx context.Context,
	q hosts.Query,
	provs []hosts.Backend,
	cacheDir string,
	cacheTTL time.Duration,
	noCache bool,
	refresh bool,
) ([]hosts.Record, error) {
	if cacheDir == "" {
		d, err := hosts.DefaultCacheDir()
		if err != nil {
			return nil, err
		}
		cacheDir = d
	}
	cachePath := filepath.Join(cacheDir, "cache.json")
	var fc *hosts.FileCache
	if !noCache {
		fc = hosts.NewFileCache(cachePath, cacheTTL)
	}
	return hosts.RunParallel(ctx, q, provs, fc, noCache, refresh, hosts.DefaultCacheKey)
}
