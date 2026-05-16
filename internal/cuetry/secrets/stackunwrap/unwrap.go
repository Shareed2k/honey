package stackunwrap

import "context"

// DataKeyUnwrapper unwraps a stack data key from defaults.secretsprovider + defaults.encryptedkey.
type DataKeyUnwrapper interface {
	Name() string
	Supports(providerURL string) bool
	Unwrap(ctx context.Context, providerURL, encryptedKey string) ([]byte, error)
}
