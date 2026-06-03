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
	ExecutorFor(r hosts.Record, reg hostexec.Registry) hostexec.Executor
}

// ResolveExecutor dispatches records to provider executors.
func (r *Registry) ResolveExecutor(rec hosts.Record, reg hostexec.Registry) hostexec.Executor {
	for _, f := range r.Factories {
		ep, ok := f.(ExecutorProvider)
		if !ok || ep.ProviderName() != rec.Provider {
			continue
		}
		return ep.ExecutorFor(rec, reg)
	}
	return nil
}
