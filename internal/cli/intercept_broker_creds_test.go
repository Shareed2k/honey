package cli

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/shareed2k/honey/internal/config"
)

// writeKubeconfigWithContext writes a minimal kubeconfig with a single context
// named ctxName pointing at server, and returns its file path.
func writeKubeconfigWithContext(t *testing.T, ctxName, server string) string {
	t.Helper()
	cfg := clientcmdapi.NewConfig()
	cfg.Clusters[ctxName] = &clientcmdapi.Cluster{Server: server}
	cfg.AuthInfos[ctxName] = &clientcmdapi.AuthInfo{Token: "test-token"}
	cfg.Contexts[ctxName] = &clientcmdapi.Context{Cluster: ctxName, AuthInfo: ctxName}
	cfg.CurrentContext = ctxName
	path := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*cfg, path))
	return path
}

// The brokered path prefers the kubeconfig context `honey kube login` wrote, so
// no separate local cluster mapping is needed.
func TestBrokeredOperatorRestConfig_UsesLoginContext(t *testing.T) {
	path := writeKubeconfigWithContext(t, "honey-prod", "https://proxy.example:6443/prod")
	t.Setenv("KUBECONFIG", path)

	rc, source, err := brokeredOperatorRestConfig(&config.File{}, "prod")
	require.NoError(t, err)
	require.Equal(t, "https://proxy.example:6443/prod", rc.Host)
	require.Contains(t, source, "honey kube login context")
	require.Contains(t, source, "honey-prod")
}

// An explicit k8s_proxy.clusters entry overrides the login context.
func TestBrokeredOperatorRestConfig_ExplicitMappingWins(t *testing.T) {
	loginPath := writeKubeconfigWithContext(t, "honey-prod", "https://proxy.example:6443/prod")
	t.Setenv("KUBECONFIG", loginPath)

	directPath := writeKubeconfigWithContext(t, "direct", "https://direct.example:6443")
	cfg := &config.File{K8sProxy: &config.K8sProxyConfig{
		Clusters: []config.K8sProxyCluster{{Name: "prod", Kubeconfig: directPath, Context: "direct"}},
	}}

	rc, source, err := brokeredOperatorRestConfig(cfg, "prod")
	require.NoError(t, err)
	require.Equal(t, "https://direct.example:6443", rc.Host)
	require.Contains(t, source, "k8s_proxy.clusters")
}

// No login context and no mapping yields a helpful, actionable error.
func TestBrokeredOperatorRestConfig_NoCredsErrors(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "kubeconfig")) // does not exist

	_, _, err := brokeredOperatorRestConfig(&config.File{}, "prod")
	require.Error(t, err)
	require.Contains(t, err.Error(), "honey kube login prod")
}

func TestBrokeredOperatorRestConfig_EmptyClusterErrors(t *testing.T) {
	_, _, err := brokeredOperatorRestConfig(&config.File{}, "  ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--cluster is required")
}
