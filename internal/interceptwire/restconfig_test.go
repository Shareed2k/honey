package interceptwire

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
)

func TestClusterKubeconfig(t *testing.T) {
	t.Parallel()
	cfg := &config.File{K8sProxy: &config.K8sProxyConfig{Clusters: []config.K8sProxyCluster{
		{Name: "prod", Kubeconfig: "/etc/kube/prod.yaml", Context: "prod-ctx"},
	}}}

	tests := []struct {
		name        string
		cfg         *config.File
		cluster     string
		wantConfig  string
		wantContext string
		wantErr     string
	}{
		{
			name:        "cluster found in k8s_proxy.clusters",
			cfg:         cfg,
			cluster:     "prod",
			wantConfig:  "/etc/kube/prod.yaml",
			wantContext: "prod-ctx",
		},
		{
			name:    "unknown cluster errors",
			cfg:     cfg,
			cluster: "staging",
			wantErr: "not defined in k8s_proxy.clusters",
		},
		{
			name:    "unknown cluster errors with nil k8s_proxy",
			cfg:     &config.File{},
			cluster: "prod",
			wantErr: "not defined in k8s_proxy.clusters",
		},
		{
			name:    "empty cluster returns empty, no error",
			cfg:     cfg,
			cluster: "",
		},
		{
			name:    "whitespace-only cluster returns empty, no error",
			cfg:     cfg,
			cluster: "   ",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kubeconfig, kubeContext, err := ClusterKubeconfig(tc.cfg, tc.cluster)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantConfig, kubeconfig)
			assert.Equal(t, tc.wantContext, kubeContext)
		})
	}
}

func TestRestConfigForCluster_UnknownClusterErrors(t *testing.T) {
	t.Parallel()
	// A --cluster name not present in k8s_proxy.clusters must error, not silently
	// fall back to the current kubeconfig context (gate/audit integrity).
	cfg := &config.File{K8sProxy: &config.K8sProxyConfig{Clusters: []config.K8sProxyCluster{{Name: "prod"}}}}
	_, err := RestConfigForCluster(cfg, "staging")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not defined in k8s_proxy.clusters")

	// Nil k8s_proxy with a named cluster also errors.
	_, err = RestConfigForCluster(&config.File{}, "prod")
	require.Error(t, err)
}
