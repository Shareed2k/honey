package honeyprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHoneySearch(t *testing.T) {
	expectedRecords := []hosts.Record{
		{
			Provider:  "honey",
			Name:      "remote-host",
			PrimaryIP: "1.2.3.4",
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/search", r.URL.Path)
		assert.Equal(t, "Bearer my-token", r.Header.Get("Authorization"))

		var req hostapi.SearchHostsInput
		err := json.NewDecoder(r.Body).Decode(&req)
		if !assert.NoError(t, err) {
			return
		}

		assert.Equal(t, "test", req.Name)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(searchResponse{
			Records: expectedRecords,
		})
	}))
	defer ts.Close()

	h := &Honey{
		Name:  "my-honey",
		URL:   ts.URL,
		Token: "my-token",
	}

	ctx := context.Background()
	q := hosts.Query{
		NameSubstring: "test",
	}

	records, err := h.Search(ctx, q)
	require.NoError(t, err)

	assert.Len(t, records, 1)
	assert.Equal(t, "remote-host", records[0].Name)
	assert.Equal(t, "1.2.3.4", records[0].PrimaryIP)
}

func TestHoneySearch_BackendFilter(t *testing.T) {
	tests := []struct {
		name            string
		honeyName       string
		queryBackends   []string
		wantFwdBackends string
	}{
		{
			name:            "own name stripped",
			honeyName:       "my-proxy",
			queryBackends:   []string{"my-proxy"},
			wantFwdBackends: "",
		},
		{
			name:            "sub-backend forwarded",
			honeyName:       "my-proxy",
			queryBackends:   []string{"gcp-prod"},
			wantFwdBackends: "gcp-prod",
		},
		{
			name:            "mixed: strip own keep sub",
			honeyName:       "my-proxy",
			queryBackends:   []string{"my-proxy", "gcp-prod"},
			wantFwdBackends: "gcp-prod",
		},
		{
			name:            "honey:name format stripped",
			honeyName:       "my-proxy",
			queryBackends:   []string{"honey:my-proxy"},
			wantFwdBackends: "",
		},
		{
			name:            "bare honey kind stripped",
			honeyName:       "my-proxy",
			queryBackends:   []string{"honey"},
			wantFwdBackends: "",
		},
		{
			name:            "empty filter",
			honeyName:       "my-proxy",
			queryBackends:   []string{},
			wantFwdBackends: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotBackends string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req hostapi.SearchHostsInput
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				gotBackends = req.Backends
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(searchResponse{Records: []hosts.Record{}})
			}))
			defer ts.Close()

			h := &Honey{Name: tt.honeyName, URL: ts.URL}
			_, err := h.Search(context.Background(), hosts.Query{Backends: tt.queryBackends})
			require.NoError(t, err)
			assert.Equal(t, tt.wantFwdBackends, gotBackends)
		})
	}
}

// TestHoneySearch_Mesh covers the honey.go call site: swapping the
// package-level meshDial (white-box only) so no real mesh/relay is touched.
// These two subtests share that package-level var, so (unlike the other
// subtests in this file) they are intentionally not run with t.Parallel().
func TestHoneySearch_Mesh(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(searchResponse{Records: []hosts.Record{}})
	}))
	defer ts.Close()

	t.Run("mesh true routes through meshDial", func(t *testing.T) {
		orig := meshDial
		var gotAddr string
		meshDial = func(ctx context.Context, meshAddr string) (net.Conn, error) {
			gotAddr = meshAddr
			var d net.Dialer
			return d.DialContext(ctx, "tcp", ts.Listener.Addr().String())
		}
		t.Cleanup(func() { meshDial = orig })

		h := &Honey{Name: "mesh-honey", URL: "http://mesh-target.invalid", Mesh: true, MeshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target"}
		_, err := h.Search(context.Background(), hosts.Query{})
		require.NoError(t, err)
		assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", gotAddr)
	})

	t.Run("mesh false uses the default dialer", func(t *testing.T) {
		orig := meshDial
		called := false
		meshDial = func(context.Context, string) (net.Conn, error) {
			called = true
			return nil, errors.New("meshDial should not be called")
		}
		t.Cleanup(func() { meshDial = orig })

		h := &Honey{Name: "plain-honey", URL: ts.URL}
		_, err := h.Search(context.Background(), hosts.Query{})
		require.NoError(t, err)
		assert.False(t, called)
	})
}
