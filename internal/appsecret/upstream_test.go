package appsecret

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestIsEncryptedUpstream(t *testing.T) {
	tests := []struct {
		name string
		val  string
		want bool
	}{
		{
			name: "encrypted upstream",
			val:  "secure:v1:some_encrypted_data",
			want: true,
		},
		{
			name: "encrypted with spaces",
			val:  "  secure:v1:data  ",
			want: true,
		},
		{
			name: "plain upstream",
			val:  "postgres://user:pass@host:5432/db",
			want: false,
		},
		{
			name: "empty string",
			val:  "",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsEncryptedUpstream(tt.val)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveUpstream(t *testing.T) {
	ctx := context.Background()

	t.Run("unencrypted_upstream", func(t *testing.T) {
		cfg := &config.File{}
		plain := "postgres://user:pass@host/db"
		got, err := ResolveUpstream(ctx, cfg, plain)
		assert.NoError(t, err)
		assert.Equal(t, plain, got)
	})

	t.Run("encrypted_upstream_fails_without_key", func(t *testing.T) {
		cfg := &config.File{}
		// This should fail to unseal because we haven't set up the keys/provider
		encrypted := "secure:v1:data"
		_, err := ResolveUpstream(ctx, cfg, encrypted)
		assert.ErrorContains(t, err, "failed to decrypt app upstream")
	})
}
