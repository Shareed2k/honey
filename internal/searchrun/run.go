package searchrun

import (
	"context"
	"path/filepath"
	"time"

	"go.uber.org/zap"

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

	zap.L().Debug(
		"starting search run",
		zap.String("cache_path", cachePath),
		zap.Duration("cache_ttl", cacheTTL),
		zap.Bool("no_cache", noCache),
		zap.Bool("refresh", refresh),
		zap.Int("providers_count", len(provs)),
	)

	var fc *hosts.FileCache
	if !noCache {
		fc = hosts.NewFileCache(cachePath, cacheTTL)
	}

	records, err := hosts.RunParallel(ctx, q, provs, fc, noCache, refresh, hosts.DefaultCacheKey)
	zap.L().Debug("completed search run", zap.Int("total_records", len(records)), zap.Error(err))
	return records, err
}
