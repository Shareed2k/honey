package interceptwire

import (
	"fmt"
	"strings"

	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

// ClusterKubeconfig resolves the kubeconfig path and context for a named
// cluster. A named cluster MUST resolve to a k8s_proxy.clusters entry:
// silently falling back to the current context would deploy the agent to a
// different cluster than the one the OPA gate authorized and the audit
// records — a gate/audit-integrity gap. An empty cluster means the standard
// current kubeconfig context (empty path/context).
func ClusterKubeconfig(cfg *config.File, cluster string) (kubeconfig, kubeContext string, err error) {
	cluster = strings.TrimSpace(cluster)
	if cluster == "" {
		return "", "", nil
	}
	if cfg.K8sProxy != nil {
		for _, c := range cfg.K8sProxy.Clusters {
			if c.Name == cluster {
				return c.Kubeconfig, c.Context, nil
			}
		}
	}
	return "", "", fmt.Errorf("intercept: cluster %q is not defined in k8s_proxy.clusters", cluster)
}

// RestConfigForCluster resolves the target cluster's REST config. When
// cluster names one of the k8s_proxy clusters, that cluster's
// kubeconfig/context is reused; otherwise the default kubeconfig loading
// rules apply (KUBECONFIG / ~/.kube/config, current context). Reusing
// k8s_proxy.clusters avoids adding a second cluster→kubeconfig mapping to the
// config.
func RestConfigForCluster(cfg *config.File, cluster string) (*rest.Config, error) {
	kubeconfig, kubeContext, err := ClusterKubeconfig(cfg, cluster)
	if err != nil {
		return nil, err
	}
	restCfg, err := k8sprovider.RestConfigForKubeconfig(kubeconfig, kubeContext)
	if err != nil {
		return nil, fmt.Errorf("intercept: resolve cluster %q kubeconfig: %w", strings.TrimSpace(cluster), err)
	}
	return restCfg, nil
}
