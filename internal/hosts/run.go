package hosts

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

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
			recs, err = p.Search(ctx, q)
			if err != nil {
				return err
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

// DefaultCacheKey serializes query fields for caching per provider.
func DefaultCacheKey(p Backend, q Query) ([]byte, error) {
	type key struct {
		Provider string `json:"provider"`
		Query    Query  `json:"query"`
	}
	return json.Marshal(key{Provider: p.ID(), Query: q})
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
