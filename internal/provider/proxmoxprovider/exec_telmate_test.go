package proxmoxprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDialTelmate(t *testing.T) {
	ctx := context.Background()

	t.Run("empty URL", func(t *testing.T) {
		b := ProxmoxBackendRuntime{}
		client, err := dialTelmate(ctx, b)
		assert.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "empty API URL")
	})

	t.Run("missing credentials", func(t *testing.T) {
		b := ProxmoxBackendRuntime{
			URL: "https://proxmox.local:8006/api2/json",
		}
		client, err := dialTelmate(ctx, b)
		assert.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "need token or user/password")
	})

	t.Run("invalid token ID", func(t *testing.T) {
		b := ProxmoxBackendRuntime{
			URL:     "https://proxmox.local:8006/api2/json",
			TokenID: "invalid-token-id-without-realm",
		}
		client, err := dialTelmate(ctx, b)
		assert.Error(t, err)
		assert.Nil(t, client)
		assert.Contains(t, err.Error(), "proxmox token id")
	})

	t.Run("valid token configuration", func(t *testing.T) {
		b := ProxmoxBackendRuntime{
			URL:      "https://proxmox.local:8006/api2/json",
			TokenID:  "root@pam!token",
			TokenSec: "secret",
		}
		client, err := dialTelmate(ctx, b)
		assert.NoError(t, err)
		assert.NotNil(t, client)
	})
}
