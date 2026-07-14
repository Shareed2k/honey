package honeyprovider_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockConfigProvider struct {
	backends []config.HoneyBackend
}

func (m *mockConfigProvider) HoneyBackends() []config.HoneyBackend {
	return m.backends
}

func (m *mockConfigProvider) HoneyBackendSlicePtr() *[]config.HoneyBackend {
	return &m.backends
}

func (m *mockConfigProvider) SetHoneyBackends(b []config.HoneyBackend) {
	m.backends = b
}

func TestBackendRows(t *testing.T) {
	t.Parallel()

	// Mock server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/backends", r.URL.Path)

		authHeader := r.Header.Get("Authorization")
		if authHeader == "Bearer test-token" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(hostapi.ListBackendsOutput{
				Backends: []config.BackendRow{
					{Kind: "ssh", Name: "remote-ssh-1", Hint: "10.0.0.1"},
					{Kind: "ssh", Name: "remote-ssh-2", Hint: "10.0.0.2"},
				},
			})
			return
		}

		if authHeader == "Bearer invalid-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// default behavior
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(hostapi.ListBackendsOutput{
			Backends: []config.BackendRow{
				{Kind: "kubernetes", Name: "remote-k8s-1", Hint: "kube-cluster"},
			},
		})
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	cfg := &mockConfigProvider{
		backends: []config.HoneyBackend{
			{Name: "honey-auth", URL: server.URL, Token: "test-token", Insecure: true},
			{Name: "honey-no-auth", URL: server.URL, Insecure: false},
			{Name: "honey-invalid", URL: server.URL, Token: "invalid-token", Insecure: false},
			{Name: "honey-offline", URL: "http://localhost:12345"}, // Should fail gracefully
		},
	}

	factory := honeyprovider.NewFactory(cfg)
	rows := factory.BackendRows()

	require.NotNil(t, rows)

	// We expect:
	// 4 "honey" backends from our config
	// 2 "ssh" backends from honey-auth
	// 1 "kubernetes" backend from honey-no-auth
	// 0 from honey-invalid (401)
	// 0 from honey-offline (connection error)

	assert.Len(t, rows, 7)

	var honeyCount, sshCount, k8sCount int
	for _, row := range rows {
		switch row.Kind {
		case "honey":
			honeyCount++
		case "ssh":
			sshCount++
		case "kubernetes":
			k8sCount++
		}
	}

	assert.Equal(t, 4, honeyCount)
	assert.Equal(t, 2, sshCount)
	assert.Equal(t, 1, k8sCount)
}

// TestMTLSBackendsSkipped verifies mTLS-managed honey backends are ignored by the
// in-process provider (owned by the device client): not listed, not fetched, and
// no executor is offered.
func TestMTLSBackendsSkipped(t *testing.T) {
	t.Parallel()

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(hostapi.ListBackendsOutput{
			Backends: []config.BackendRow{{Kind: "ssh", Name: "remote", Hint: "10.0.0.9"}},
		})
	}))
	defer server.Close()

	cfg := &mockConfigProvider{
		backends: []config.HoneyBackend{
			{Name: "go-honey", URL: server.URL, Token: "t"},
			{Name: "mtls-honey", URL: server.URL, Token: "t", MTLS: true},
		},
	}
	factory := honeyprovider.NewFactory(cfg)

	// FromConfig: only the non-mTLS backend becomes a search provider.
	providers := factory.FromConfig(searchrun.ProviderOverrides{})
	assert.Len(t, providers, 1)

	// BackendRows: the mTLS backend's own "honey" row is absent; only go-honey
	// (1 honey row) + its 1 fetched ssh row.
	rows := factory.BackendRows()
	var honeyNames []string
	for _, r := range rows {
		if r.Kind == "honey" {
			honeyNames = append(honeyNames, r.Name)
		}
	}
	assert.Equal(t, []string{"go-honey"}, honeyNames)
	// Only the non-mTLS backend was fetched over HTTP.
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits))

	// ExecutorFor: an mTLS upstream yields no (token-based) WS executor.
	ep, ok := factory.(searchrun.ExecutorProvider)
	require.True(t, ok)
	rec := hosts.Record{Meta: map[string]string{"honey_upstream_backend": "mtls-honey"}}
	assert.Nil(t, ep.ExecutorFor(rec, nil))
}

// TestFromConfig_MeshFieldsPropagate verifies Mesh/MeshAddr are threaded from
// config.HoneyBackend into the constructed *honeyprovider.Honey unchanged.
func TestFromConfig_MeshFieldsPropagate(t *testing.T) {
	t.Parallel()

	cfg := &mockConfigProvider{
		backends: []config.HoneyBackend{
			{Name: "mesh-honey", URL: "http://mesh.example.com", Mesh: true, MeshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target"},
		},
	}
	factory := honeyprovider.NewFactory(cfg)

	providers := factory.FromConfig(searchrun.ProviderOverrides{})
	require.Len(t, providers, 1)

	h, ok := providers[0].(*honeyprovider.Honey)
	require.True(t, ok)
	assert.True(t, h.Mesh)
	assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", h.MeshAddr)
}

// TestExecutorFor_MeshFieldsPropagate verifies Mesh/MeshAddr are threaded from
// config.HoneyBackend into the constructed *honeyprovider.Executor unchanged.
func TestExecutorFor_MeshFieldsPropagate(t *testing.T) {
	t.Parallel()

	cfg := &mockConfigProvider{
		backends: []config.HoneyBackend{
			{Name: "mesh-honey", URL: "http://mesh.example.com", Mesh: true, MeshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target"},
		},
	}
	factory := honeyprovider.NewFactory(cfg)

	ep, ok := factory.(searchrun.ExecutorProvider)
	require.True(t, ok)
	rec := hosts.Record{Meta: map[string]string{"honey_upstream_backend": "mesh-honey"}}
	ex, ok := ep.ExecutorFor(rec, nil).(*honeyprovider.Executor)
	require.True(t, ok)
	assert.True(t, ex.Mesh)
	assert.Equal(t, "/ip4/1.2.3.4/tcp/4001/p2p/target", ex.MeshAddr)
}

// TestMeshBackendsNotSkipped verifies mesh-flagged honey backends are, unlike
// MTLS-managed ones, never excluded from FromConfig/BackendRows just because
// this process's own mesh dial isn't (yet) ready — that's a normal per-call
// network error, not a structural unreachability like an MTLS credential
// owned by another process. See honeyFactory.FromConfig's Mesh field comment.
func TestMeshBackendsNotSkipped(t *testing.T) {
	t.Parallel()

	cfg := &mockConfigProvider{
		backends: []config.HoneyBackend{
			{Name: "mesh-honey", URL: "http://mesh-target.invalid", Mesh: true, MeshAddr: "/ip4/1.2.3.4/tcp/4001/p2p/target"},
		},
	}
	factory := honeyprovider.NewFactory(cfg)

	// FromConfig: the mesh backend is included even though no mesh has been
	// started in this process (meshnet.Start is never called by this package).
	providers := factory.FromConfig(searchrun.ProviderOverrides{})
	assert.Len(t, providers, 1)

	// BackendRows: its own "honey" row is present too (the fetch of its
	// sub-backends will simply fail quietly, like any other unreachable
	// backend, since nothing is listening at mesh-target.invalid).
	rows := factory.BackendRows()
	var honeyNames []string
	for _, r := range rows {
		if r.Kind == "honey" {
			honeyNames = append(honeyNames, r.Name)
		}
	}
	assert.Equal(t, []string{"mesh-honey"}, honeyNames)
}
