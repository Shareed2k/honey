package honeyprovider_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
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
