package proxmoxprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestProxmoxSearch_ConfigValidation(t *testing.T) {
	ctx := context.Background()
	query := hosts.Query{}

	t.Run("empty URL", func(t *testing.T) {
		p := &Proxmox{}
		records, err := p.Search(ctx, query)
		assert.NoError(t, err)
		assert.Nil(t, records) // unconfigured should return nil, nil
	})

	t.Run("missing credentials", func(t *testing.T) {
		p := &Proxmox{
			URL: "https://proxmox.local:8006/api2/json",
		}
		records, err := p.Search(ctx, query)
		assert.Error(t, err)
		assert.Nil(t, records)
		assert.Contains(t, err.Error(), "requires token or user/password")
	})

	t.Run("invalid token ID", func(t *testing.T) {
		p := &Proxmox{
			URL:     "https://proxmox.local:8006/api2/json",
			TokenID: "invalid-token-id-without-realm",
		}
		records, err := p.Search(ctx, query)
		assert.Error(t, err)
		assert.Nil(t, records)
		assert.Contains(t, err.Error(), "proxmox token id")
	})
}
