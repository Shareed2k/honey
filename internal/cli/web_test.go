package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
)

// writeInterceptTestKubeconfig writes a minimal, valid kubeconfig with a
// single cluster/context so buildInterceptBroker's per-cluster
// k8sprovider.RestConfigForKubeconfig resolution has something real to load —
// mirrors internal/provider/k8sprovider's own test fixture.
func writeInterceptTestKubeconfig(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	data := `
apiVersion: v1
kind: Config
clusters:
- name: cluster-a
  cluster:
    server: https://cluster-a.example.com
contexts:
- name: context-a
  context:
    cluster: cluster-a
current-context: context-a
users: []
`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	return path
}

// interceptTestConfig returns a minimal *config.File with intercept enabled
// against one k8s_proxy cluster (backed by a real, on-disk kubeconfig so
// buildInterceptBroker's cluster resolution succeeds) and the given
// session_store/session_store_dsn.
func interceptTestConfig(t *testing.T, sessionStore, sessionStoreDSN string) *config.File {
	t.Helper()

	return &config.File{
		Intercept: &config.InterceptConfig{
			Enabled:         true,
			SessionStore:    sessionStore,
			SessionStoreDSN: sessionStoreDSN,
		},
		K8sProxy: &config.K8sProxyConfig{
			Clusters: []config.K8sProxyCluster{
				{Name: "cluster-a", Kubeconfig: writeInterceptTestKubeconfig(t), Context: "context-a"},
			},
		},
	}
}

func TestBuildInterceptBroker_SessionStoreSelection(t *testing.T) {
	t.Run("memory default", func(t *testing.T) {
		cfg := interceptTestConfig(t, "", "")
		broker, _, err := buildInterceptBroker(cfg, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, broker)
	})

	t.Run("sqlite with dsn", func(t *testing.T) {
		dsn := filepath.Join(t.TempDir(), "s.db")
		cfg := interceptTestConfig(t, "sqlite", dsn)
		broker, _, err := buildInterceptBroker(cfg, nil, nil)
		require.NoError(t, err)
		require.NotNil(t, broker)
	})

	t.Run("sqlite without dsn errors", func(t *testing.T) {
		cfg := interceptTestConfig(t, "sqlite", "")
		broker, _, err := buildInterceptBroker(cfg, nil, nil)
		require.Error(t, err)
		require.Nil(t, broker)
	})

	t.Run("unknown store errors", func(t *testing.T) {
		cfg := interceptTestConfig(t, "bogus", "")
		broker, _, err := buildInterceptBroker(cfg, nil, nil)
		require.Error(t, err)
		require.Nil(t, broker)
	})
}
