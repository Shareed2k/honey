package cuetry

import (
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestSecretResolverOptionsFromHoney(t *testing.T) {
	cfg := &config.File{
		Defaults: config.Defaults{
			SecretsProvider: "gcpkms://projects/p/locations/l/keyRings/r/cryptoKeys/k",
			EncryptedKey:    "blob",
		},
	}
	o := SecretResolverOptionsFromHoney(cfg)
	if o.SecretsProvider != cfg.Defaults.SecretsProvider || o.EncryptedKey != cfg.Defaults.EncryptedKey {
		t.Fatalf("%+v", o)
	}
}
