package searchrun

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/shareed2k/honey/internal/config"
)

// BackendConfigRegistry can be implemented by a ProviderFactory so backend
// CRUD handlers can locate the matching cfg.Backends.<kind> slice dynamically.
type BackendConfigRegistry interface {
	BackendKind() string
	BackendSlicePtr() any
}

func (r *Registry) registerBackendSlice(kind string, getter func(cfg *config.File) reflect.Value) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || getter == nil {
		return
	}
	r.backendSliceByKind[kind] = getter
}

// RegisteredBackendKinds returns registered backend YAML kind names, sorted.
func (r *Registry) RegisteredBackendKinds() []string {
	kinds := make([]string, 0, len(r.backendSliceByKind))
	for kind := range r.backendSliceByKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// GetBackendSliceByKind resolves cfg.Backends.<kind> as a reflect.Value slice.
func (r *Registry) GetBackendSliceByKind(kind string) (reflect.Value, error) {
	cfg := config.Get()
	if cfg == nil {
		return reflect.Value{}, fmt.Errorf("nil config")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	getter, ok := r.backendSliceByKind[kind]
	if !ok {
		return reflect.Value{}, fmt.Errorf("unknown backend kind %q (use %s)", kind, strings.Join(r.RegisteredBackendKinds(), ", "))
	}
	slice := getter(cfg)
	if !slice.IsValid() || slice.Kind() != reflect.Slice {
		return reflect.Value{}, fmt.Errorf("backend registry getter for %q must return a slice", kind)
	}
	return slice, nil
}
