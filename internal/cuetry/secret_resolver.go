package cuetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry/secrets"
	"github.com/shareed2k/honey/internal/plugins"
)

// SecretResolver resolves recipe secret refs (secure:v1:…) to plaintext at execute time.
type SecretResolver interface {
	Handles(ref string) bool
	Resolve(ctx context.Context, ref string) (string, error)
}

// SecretResolverOptions configures the default secret resolver.
type SecretResolverOptions struct {
	SymmetricDataKey []byte
	SecretsProvider  string
	EncryptedKey     string
	AgeIdentityFile  string
}

// SecretResolverOptionsFromHoney maps honey YAML defaults into resolver options.
func SecretResolverOptionsFromHoney(cfg *config.File) SecretResolverOptions {
	o := SecretResolverOptions{}
	if cfg != nil {
		o.SecretsProvider = strings.TrimSpace(cfg.Defaults.SecretsProvider)
		o.EncryptedKey = strings.TrimSpace(cfg.Defaults.EncryptedKey)
	}
	if p := strings.TrimSpace(os.Getenv("HONEY_AGE_IDENTITY_FILE")); p != "" {
		o.AgeIdentityFile = p
	}
	return o
}

// NewSecretResolver builds the default resolver for recipe execution.
func NewSecretResolver(opts SecretResolverOptions) (SecretResolver, error) {
	return NewSecretResolverWithPlugins(opts, nil)
}

// NewSecretResolverWithPlugins appends WASM plugin secret backends when mgr is non-nil.
func NewSecretResolverWithPlugins(opts SecretResolverOptions, mgr *plugins.Manager) (SecretResolver, error) {
	secOpts := secrets.Options{
		SymmetricDataKey: opts.SymmetricDataKey,
		SecretsProvider:  opts.SecretsProvider,
		EncryptedKey:     opts.EncryptedKey,
		AgeIdentityFile:  opts.AgeIdentityFile,
	}
	if mgr != nil {
		secOpts.ExtraBackends = mgr.SecretRefBackends()
	}
	inner, err := secrets.NewResolver(secOpts)
	if err != nil {
		return nil, err
	}
	// A fresh resolver is built per recipe run, so memoizing per instance =
	// per-run caching. Without this, the same secret ref is re-resolved for
	// every (step × host) — each a live KMS/Vault/SM/K8s round-trip.
	return newMemoizingResolver(inner), nil
}

// memoResult caches one resolved ref (value or error) for a resolver's lifetime.
type memoResult struct {
	val string
	err error
}

// memoizingResolver wraps a SecretResolver with a per-instance cache. Secrets are
// immutable for a run, so the same ref always yields the same value; this turns
// N_hosts × N_steps re-resolutions of a ref into a single backend call.
// singleflight collapses concurrent first-time resolves of the same ref (many
// hosts starting a step together) into one backend call, not one per goroutine.
type memoizingResolver struct {
	inner SecretResolver
	cache sync.Map // ref string -> memoResult
	sf    singleflight.Group
}

func newMemoizingResolver(inner SecretResolver) SecretResolver {
	if inner == nil {
		return nil
	}
	return &memoizingResolver{inner: inner}
}

// Handles delegates (routing check; not a resolution round-trip).
func (m *memoizingResolver) Handles(ref string) bool { return m.inner.Handles(ref) }

// Resolve returns the cached result if present, else resolves once (guarded by
// singleflight) and caches it. Errors are cached too — a failing ref fails the
// run regardless, and re-hitting the backend N times would only slow that down.
func (m *memoizingResolver) Resolve(ctx context.Context, ref string) (string, error) {
	if v, ok := m.cache.Load(ref); ok {
		r := v.(memoResult)
		return r.val, r.err
	}
	out, _, _ := m.sf.Do(ref, func() (any, error) {
		if v, ok := m.cache.Load(ref); ok {
			return v.(memoResult), nil
		}
		val, err := m.inner.Resolve(ctx, ref)
		res := memoResult{val: val, err: err}
		m.cache.Store(ref, res)
		return res, nil
	})
	r := out.(memoResult)
	return r.val, r.err
}

// StaticSecretResolver provides a static map of secrets for testing and simple use cases.
type StaticSecretResolver map[string]string

// Handles returns true if the reference exists in the static map.
func (m StaticSecretResolver) Handles(ref string) bool {
	_, ok := m[ref]
	return ok
}

// Resolve returns the value from the static map if it exists.
func (m StaticSecretResolver) Resolve(_ context.Context, ref string) (string, error) {
	if val, ok := m[ref]; ok {
		return val, nil
	}
	return "", fmt.Errorf("secret not found")
}
