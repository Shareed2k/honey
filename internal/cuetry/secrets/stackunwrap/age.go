package stackunwrap

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"

	"github.com/shareed2k/honey/internal/safepath"
)

// Age unwraps a stack data key using age identities from identityFile.
// secretsprovider age:// — armored ciphertext in encryptedkey.
// secretsprovider age-file://path — ciphertext read from path (encryptedkey ignored).
type Age struct {
	IdentityFile string
}

// Name implements [DataKeyUnwrapper].
func (a Age) Name() string { return "age" }

// Supports implements [DataKeyUnwrapper].
func (a Age) Supports(providerURL string) bool {
	p := strings.TrimSpace(providerURL)
	return strings.HasPrefix(p, "age://") || strings.HasPrefix(p, "age-file://")
}

func (a Age) Unwrap(ctx context.Context, providerURL, encryptedKey string) ([]byte, error) {
	_ = ctx
	if strings.TrimSpace(a.IdentityFile) == "" {
		return nil, fmt.Errorf("age stack provider requires AgeIdentityFile or HONEY_AGE_IDENTITY_FILE")
	}
	ids, err := loadAgeIdentities(a.IdentityFile)
	if err != nil {
		return nil, err
	}
	var armored []byte
	p := strings.TrimSpace(providerURL)
	switch {
	case strings.HasPrefix(p, "age-file://"):
		path := strings.TrimSpace(p[len("age-file://"):])
		if path == "" {
			return nil, fmt.Errorf("age-file: missing path")
		}
		armored, err = safepath.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("age-file: %w", err)
		}
	case strings.HasPrefix(p, "age://"):
		armored = []byte(strings.TrimSpace(encryptedKey))
		if len(armored) == 0 {
			return nil, fmt.Errorf("age:// requires non-empty encryptedkey (armored ciphertext)")
		}
	default:
		return nil, fmt.Errorf("age: unsupported provider URL %q", providerURL)
	}
	plain, err := decryptAgeToBytes(ids, armored)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func loadAgeIdentities(path string) ([]age.Identity, error) {
	b, err := safepath.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("age identity file: %w", err)
	}
	ids, err := age.ParseIdentities(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("age identities: %w", err)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("age identity file %q: no identities", path)
	}
	return ids, nil
}

func decryptAgeToBytes(ids []age.Identity, ciphertext []byte) ([]byte, error) {
	src := bytes.NewReader(ciphertext)
	var dec interface{ Read([]byte) (int, error) }
	if bytes.HasPrefix(ciphertext, []byte(armor.Header)) {
		dec = armor.NewReader(src)
	} else {
		dec = src
	}
	r, err := age.Decrypt(dec, ids...)
	if err != nil {
		return nil, fmt.Errorf("age decrypt stack key: %w", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
