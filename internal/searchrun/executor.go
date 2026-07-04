package searchrun

import (
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// ExecutorProvider is optionally implemented by factories that provide a custom exec transport.
// The resolver routes records by calling HandlesRecord before calling ExecutorFor.
type ExecutorProvider interface {
	// HandlesRecord reports whether this factory can provide an executor for the record.
	HandlesRecord(r hosts.Record) bool
	// ExecutorFor returns the executor for r, or nil to fall through to SSH.
	ExecutorFor(r hosts.Record, reg hostexec.Registry) hostexec.Executor
}

// ResolveExecutor dispatches records to provider executors.
func (r *Registry) ResolveExecutor(rec hosts.Record, reg hostexec.Registry) hostexec.Executor {
	for _, f := range r.Factories {
		ep, ok := f.(ExecutorProvider)
		if !ok || !ep.HandlesRecord(rec) {
			continue
		}
		return ep.ExecutorFor(rec, reg)
	}
	return nil
}
