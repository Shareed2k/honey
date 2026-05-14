package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// RunParallel executes enabled providers in parallel with optional disk cache.
func RunParallel(
	ctx context.Context,
	q Query,
	provs []Backend,
	fc *FileCache,
	noCache bool,
	refresh bool,
	cacheKeyFn func(p Backend, q Query) ([]byte, error),
) ([]Record, error) {
	if len(provs) == 0 {
		return nil, errors.New("no providers configured")
	}
	enabled := filterProviders(q, provs)
	if len(enabled) == 0 {
		return nil, errors.New("no providers match --provider filter")
	}

	g, ctx := errgroup.WithContext(ctx)
	results := make([][]Record, len(enabled))
	for i, p := range enabled {
		i, p := i, p
		g.Go(func() error {
			var recs []Record
			useCache := fc != nil && !noCache && cacheKeyFn != nil
			var key string
			if useCache {
				payload, kerr := cacheKeyFn(p, q)
				if kerr != nil {
					return kerr
				}
				key = CacheKeySHA256(p.ID(), payload)
				if !refresh {
					if got, ok, cerr := fc.Get(key); cerr == nil && ok {
						results[i] = got
						return nil
					}
				}
			}

			var err error
			zap.L().Debug("provider search start", zap.String("provider", p.ID()), zap.String("backend", p.BackendName()))
			recs, err = p.Search(ctx, q)
			if err != nil {
				zap.L().Error("provider search failed", zap.String("provider", p.ID()), zap.Error(err))
				return err
			}
			zap.L().Debug("provider search success", zap.String("provider", p.ID()), zap.String("backend", p.BackendName()), zap.Int("found", len(recs)))
			
			bName := p.BackendName()
			if bName == "" {
				bName = p.ID()
			}
			for j := range recs {
				if recs[j].Meta == nil {
					recs[j].Meta = make(map[string]string)
				}
				recs[j].Meta["backend_name"] = bName
			}

			if useCache && key != "" {
				_ = fc.Set(key, recs)
			}
			results[i] = recs
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	var merged []Record
	for _, sl := range results {
		merged = append(merged, sl...)
	}
	return MergeDedupe(merged), nil
}

func filterProviders(q Query, provs []Backend) []Backend {
	if len(q.Providers) == 0 {
		return provs
	}
	var out []Backend
	for _, p := range provs {
		if slices.Contains(q.Providers, p.ID()) {
			out = append(out, p)
		}
	}
	return out
}

// DefaultCacheKey serializes query fields for caching per provider instance.
func DefaultCacheKey(p Backend, q Query) ([]byte, error) {
	type key struct {
		Provider string `json:"provider"`
		Identity string `json:"identity,omitempty"`
		Query    Query  `json:"query"`
	}
	return json.Marshal(key{Provider: p.ID(), Identity: p.CacheIdentity(), Query: q})
}

// ParseProviders splits comma list; empty means all.
func ParseProviders(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
