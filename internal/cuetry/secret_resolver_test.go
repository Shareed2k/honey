package cuetry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/stretchr/testify/require"
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

func TestRecipeSecret_UnmarshalString(t *testing.T) {
	var rs RecipeSecret
	err := json.Unmarshal([]byte(`"secure:v1:hello"`), &rs)
	require.NoError(t, err)
	require.Equal(t, "secure:v1:hello", rs.Ref)
	require.Equal(t, "secure:v1:hello", rs.StringRef())
}

func TestRecipeSecret_UnmarshalVault(t *testing.T) {
	var rs RecipeSecret
	err := json.Unmarshal([]byte(`{"vault": {"path": "secret/data/my/db", "key": "password"}}`), &rs)
	require.NoError(t, err)
	require.NotNil(t, rs.Vault)
	require.Equal(t, "secret/data/my/db", rs.Vault.Path)
	require.Equal(t, "password", rs.Vault.Key)
	require.Equal(t, "vault:secret/data/my/db#password", rs.StringRef())
}

func TestRecipeSecret_UnmarshalAws(t *testing.T) {
	var rs RecipeSecret
	err := json.Unmarshal([]byte(`{"aws": {"secret_id": "my-db-cred", "version": "v1", "key": "password"}}`), &rs)
	require.NoError(t, err)
	require.NotNil(t, rs.Aws)
	require.Equal(t, "my-db-cred", rs.Aws.SecretID)
	// Output may vary depending on map order unless we force it, but StringRef uses deterministic appending.
	ref := rs.StringRef()
	require.True(t, strings.HasPrefix(ref, "aws-sm:my-db-cred?"))
	require.Contains(t, ref, "version=v1")
	require.Contains(t, ref, "key=password")
}

func TestRecipeSecret_UnmarshalGcp(t *testing.T) {
	var rs RecipeSecret
	err := json.Unmarshal([]byte(`{"gcp": {"secret": "my-db-cred", "project": "my-project", "version": "latest"}}`), &rs)
	require.NoError(t, err)
	require.NotNil(t, rs.Gcp)
	require.Equal(t, "my-db-cred", rs.Gcp.Secret)
	ref := rs.StringRef()
	require.True(t, strings.HasPrefix(ref, "gcp-sm:my-db-cred?"))
	require.Contains(t, ref, "project=my-project")
	require.Contains(t, ref, "version=latest")
}
