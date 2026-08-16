package k8sprovider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/hosts"
)

// writeTestKubeconfig writes a minimal, valid kubeconfig with two contexts so
// RestConfigForKubeconfig has something real to load and override.
func writeTestKubeconfig(t *testing.T) string {
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
- name: cluster-b
  cluster:
    server: https://cluster-b.example.com
contexts:
- name: context-a
  context:
    cluster: cluster-a
- name: context-b
  context:
    cluster: cluster-b
current-context: context-a
users: []
`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
	return path
}

func TestRestConfigForKubeconfig_ExplicitPathAndContext(t *testing.T) {
	path := writeTestKubeconfig(t)

	cfg, err := RestConfigForKubeconfig(path, "context-b")
	require.NoError(t, err)
	require.Equal(t, "https://cluster-b.example.com", cfg.Host)
}

func TestRestConfigForKubeconfig_ExplicitPathCurrentContext(t *testing.T) {
	path := writeTestKubeconfig(t)

	cfg, err := RestConfigForKubeconfig(path, "")
	require.NoError(t, err)
	require.Equal(t, "https://cluster-a.example.com", cfg.Host)
}

func TestRestConfigForKubeconfig_UnknownContextErrors(t *testing.T) {
	path := writeTestKubeconfig(t)

	_, err := RestConfigForKubeconfig(path, "does-not-exist")
	require.Error(t, err)
}

func TestNamespaceForKubeconfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	data := `
apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: https://c.example.com
contexts:
- name: ctx-ns
  context:
    cluster: c
    namespace: team-a
- name: ctx-no-ns
  context:
    cluster: c
current-context: ctx-ns
users: []
`
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))

	t.Run("current context namespace", func(t *testing.T) {
		ns, err := NamespaceForKubeconfig(path, "")
		require.NoError(t, err)
		require.Equal(t, "team-a", ns)
	})

	t.Run("explicit context namespace", func(t *testing.T) {
		ns, err := NamespaceForKubeconfig(path, "ctx-ns")
		require.NoError(t, err)
		require.Equal(t, "team-a", ns)
	})

	t.Run("context without namespace falls back to default", func(t *testing.T) {
		ns, err := NamespaceForKubeconfig(path, "ctx-no-ns")
		require.NoError(t, err)
		require.Equal(t, "default", ns)
	})
}

func TestK8sClientConfigFromRecord_DelegatesToRestConfigForKubeconfig(t *testing.T) {
	path := writeTestKubeconfig(t)

	r := hosts.Record{Meta: map[string]string{
		"kubeconfig":   path,
		"kube_context": "context-b",
	}}

	cfg, err := k8sClientConfigFromRecord(r)
	require.NoError(t, err)
	require.Equal(t, "https://cluster-b.example.com", cfg.Host)
}
