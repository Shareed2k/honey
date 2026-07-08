package consulprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestConsul_ID(t *testing.T) {
	c := Consul{}
	assert.Equal(t, "consul", c.ID())
}

func TestConsul_BackendName(t *testing.T) {
	c := Consul{Name: "  my-consul  "}
	assert.Equal(t, "my-consul", c.BackendName())
}

func TestConsul_CacheIdentity(t *testing.T) {
	c := Consul{Name: "my-consul", Addr: "127.0.0.1", Datacenter: "dc1"}
	assert.Equal(t, "my-consul\x1e127.0.0.1\x1edc1", c.CacheIdentity())
}

func TestConsul_Search(t *testing.T) {
	// Start a mock Consul HTTP API server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mock response for /v1/catalog/nodes
		if r.URL.Path != "/v1/catalog/nodes" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		nodes := []map[string]interface{}{
			{
				"Node":       "web-1",
				"Address":    "10.0.0.1",
				"Datacenter": "dc1",
				"Meta": map[string]string{
					"env": "prod",
				},
			},
			{
				"Node":       "db-1",
				"Address":    "10.0.0.2",
				"Datacenter": "dc1",
			},
			{
				"Node":       "no-ip",
				"Address":    "",
				"Datacenter": "dc1",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(nodes)
	}))
	defer ts.Close()

	ctx := context.Background()

	t.Run("match_all", func(t *testing.T) {
		c := Consul{
			Addr:       ts.URL,
			Datacenter: "dc1",
		}
		q := hosts.Query{}
		res, err := c.Search(ctx, q)
		require.NoError(t, err)
		assert.Len(t, res, 2) // "no-ip" is skipped
		assert.Equal(t, "web-1", res[0].Name)
		assert.Equal(t, "10.0.0.1", res[0].PrimaryIP)
		assert.Equal(t, "prod", res[0].Meta["label_env"])
		assert.Equal(t, "dc1", res[0].Meta["datacenter"])
	})

	t.Run("match_substring", func(t *testing.T) {
		c := Consul{
			Addr: ts.URL,
		}
		q := hosts.Query{NameSubstring: "web"}
		res, err := c.Search(ctx, q)
		require.NoError(t, err)
		assert.Len(t, res, 1)
		assert.Equal(t, "web-1", res[0].Name)
	})

	t.Run("match_regex_error", func(t *testing.T) {
		c := Consul{Addr: ts.URL}
		q := hosts.Query{NameRegex: "[invalid-regex"}
		_, err := c.Search(ctx, q)
		assert.Error(t, err)
	})

	t.Run("env_vars", func(t *testing.T) {
		os.Setenv("CONSUL_HTTP_ADDR", ts.URL)
		os.Setenv("CONSUL_HTTP_TOKEN", "token")
		defer func() {
			os.Unsetenv("CONSUL_HTTP_ADDR")
			os.Unsetenv("CONSUL_HTTP_TOKEN")
		}()

		c := Consul{}
		q := hosts.Query{NameSubstring: "db"}
		res, err := c.Search(ctx, q)
		require.NoError(t, err)
		assert.Len(t, res, 1)
		assert.Equal(t, "db-1", res[0].Name)
	})

	t.Run("bad_address", func(t *testing.T) {
		c := Consul{Addr: "http://127.0.0.1:0"} // bad address
		q := hosts.Query{}
		_, err := c.Search(ctx, q)
		assert.Error(t, err)
	})
}
