package searchrun

import (
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// ExecutorProvider is optionally implemented by factories that provide a custom exec transport.
// The resolver routes records by calling HandlesRecord before calling ExecutorFor.
//
// HandlesRecord is the single source of truth: it must report true only when this
// factory can actually produce an executor for the record, and in that case
// ExecutorFor must return a non-nil executor. A nil from ExecutorFor is treated as
// "declined" and resolution continues to the next factory (rather than dead-ending
// at the SSH fallback), so a factory can never silently claim a record it cannot
// serve.
type ExecutorProvider interface {
	// HandlesRecord reports whether this factory can provide an executor for the record.
	HandlesRecord(r hosts.Record) bool
	// ExecutorFor returns the executor for r; non-nil whenever HandlesRecord is true.
	ExecutorFor(r hosts.Record, reg hostexec.Registry) hostexec.Executor
}

// ResolveExecutor dispatches records to provider executors. A factory that claims
// a record (HandlesRecord true) but yields no executor (ExecutorFor nil) is treated
// as declining, and resolution continues to the next factory.
func (r *Registry) ResolveExecutor(rec hosts.Record, reg hostexec.Registry) hostexec.Executor {
	for _, f := range r.Factories {
		ep, ok := f.(ExecutorProvider)
		if !ok || !ep.HandlesRecord(rec) {
			continue
		}
		if ex := ep.ExecutorFor(rec, reg); ex != nil {
			return ex
		}
		// Claimed but produced no executor: decline, try the next factory.
	}
	return nil
}
