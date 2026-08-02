package localprovider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestLocal_ID(t *testing.T) {
	l := Local{}
	assert.Equal(t, "local", l.ID())
}

func TestLocal_BackendName(t *testing.T) {
	l := Local{Name: "  my-local  "}
	assert.Equal(t, "my-local", l.BackendName())
}

func TestLocal_CacheIdentity(t *testing.T) {
	l := Local{Name: "  my-local  "}
	assert.Equal(t, "my-local", l.CacheIdentity())
}

func TestLocal_Search(t *testing.T) {
	l := Local{
		Name: "test",
		Hosts: []config.LocalHost{
			{
				Name:      "web-1",
				PrimaryIP: "10.0.0.1",
				SSHUser:   "admin",
				Meta:      map[string]string{"env": "prod"},
			},
			{
				Name:      "db-1",
				PrimaryIP: "10.0.0.2",
			},
		},
	}

	ctx := context.Background()

	t.Run("match_all", func(t *testing.T) {
		q := hosts.Query{}
		res, err := l.Search(ctx, q)
		require.NoError(t, err)
		assert.Len(t, res, 2)
		assert.Equal(t, "web-1", res[0].Name)
		assert.Equal(t, "10.0.0.1", res[0].PrimaryIP)
		assert.Equal(t, "admin", res[0].Meta["ssh_user"])
		assert.Equal(t, "prod", res[0].Meta["env"])
	})

	t.Run("match_substring", func(t *testing.T) {
		q := hosts.Query{NameSubstring: "web"}
		res, err := l.Search(ctx, q)
		require.NoError(t, err)
		assert.Len(t, res, 1)
		assert.Equal(t, "web-1", res[0].Name)
	})

	t.Run("match_regex_error", func(t *testing.T) {
		q := hosts.Query{NameRegex: "[invalid-regex"}
		_, err := l.Search(ctx, q)
		assert.Error(t, err)
	})
}
