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
	BackendSlicePtr(cfg *config.File) any
}

var backendSliceByKind = map[string]func(cfg *config.File) reflect.Value{}

func registerBackendSlice(kind string, getter func(cfg *config.File) reflect.Value) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || getter == nil {
		return
	}
	backendSliceByKind[kind] = getter
}

// RegisteredBackendKinds returns registered backend YAML kind names, sorted.
func RegisteredBackendKinds() []string {
	kinds := make([]string, 0, len(backendSliceByKind))
	for kind := range backendSliceByKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

// GetBackendSliceByKind resolves cfg.Backends.<kind> as a reflect.Value slice.
func GetBackendSliceByKind(cfg *config.File, kind string) (reflect.Value, error) {
	if cfg == nil {
		return reflect.Value{}, fmt.Errorf("nil config")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	getter, ok := backendSliceByKind[kind]
	if !ok {
		return reflect.Value{}, fmt.Errorf("unknown backend kind %q (use %s)", kind, strings.Join(RegisteredBackendKinds(), ", "))
	}
	slice := getter(cfg)
	if !slice.IsValid() || slice.Kind() != reflect.Slice {
		return reflect.Value{}, fmt.Errorf("backend registry getter for %q must return a slice", kind)
	}
	return slice, nil
}
