package searchrun

import (
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// ExecutorProvider is optionally implemented by factories that provide a custom exec transport.
// The resolver routes records by ProviderName() before calling ExecutorFor, so ExecutorFor
// never needs to re-check the provider — only record-specific metadata (kind, etc.).
type ExecutorProvider interface {
	// ProviderName returns the r.Provider value this factory owns.
	ProviderName() string
	// ExecutorFor returns the executor for r, or nil to fall through to SSH.
	ExecutorFor(r hosts.Record) hostexec.Executor
}

func init() {
	hostexec.SetExecutorResolver(func(r hosts.Record) hostexec.Executor {
		for _, f := range factories {
			ep, ok := f.(ExecutorProvider)
			if !ok || ep.ProviderName() != r.Provider {
				continue
			}
			return ep.ExecutorFor(r)
		}
		return nil
	})
}
