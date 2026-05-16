package stack

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
	"github.com/shareed2k/honey/internal/cuetry/secrets/stackunwrap"
)

// UnwrapFunc unwraps secretsprovider + encryptedkey to raw stack key bytes.
type UnwrapFunc func(ctx context.Context, providerURL, encryptedKey string) ([]byte, error)

// DeferredSecure unwraps the stack data key on first secure:… resolution (lazy).
type DeferredSecure struct {
	provider     string
	encryptedKey string
	unwrap       UnwrapFunc

	mu        sync.Mutex
	dataKey   []byte
	unwrapErr error
}

// NewDeferred returns a [ref.Backend] that unwraps secretsprovider/encryptedkey on first secure:… resolve.
func NewDeferred(secretsProvider, encryptedKey string, reg *stackunwrap.Registry) ref.Backend {
	var fn UnwrapFunc
	if reg != nil {
		fn = reg.Unwrap
	}
	return &DeferredSecure{
		provider:     strings.TrimSpace(secretsProvider),
		encryptedKey: strings.TrimSpace(encryptedKey),
		unwrap:       fn,
	}
}

// Name implements [ref.Backend].
func (*DeferredSecure) Name() string { return "honey-secure" }

// Handles implements [ref.Backend].
func (d *DeferredSecure) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "secure:")
}

// Resolve implements [ref.Backend].
func (d *DeferredSecure) Resolve(ctx context.Context, ref string) (string, error) {
	inner := strings.TrimSpace(ref[len("secure:"):])
	if err := d.ensureDataKey(ctx); err != nil {
		return "", err
	}
	return DecryptSymmetricV1(d.dataKey, inner)
}

func (d *DeferredSecure) ensureDataKey(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.unwrapErr != nil {
		return d.unwrapErr
	}
	if len(d.dataKey) > 0 {
		return nil
	}
	if d.unwrap == nil {
		d.unwrapErr = fmt.Errorf("stack unwrap: no registry configured")
		return d.unwrapErr
	}
	k, err := d.unwrap(ctx, d.provider, d.encryptedKey)
	if err != nil {
		d.unwrapErr = err
		return err
	}
	if len(k) != SymmetricKeyBytes {
		d.unwrapErr = fmt.Errorf("stack unwrap: expected %d-byte data key, got %d", SymmetricKeyBytes, len(k))
		return d.unwrapErr
	}
	d.dataKey = k
	return nil
}
