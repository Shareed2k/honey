package cli

import (
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/policy"
)

// buildInterceptDeps wires the intercept.Deps a session needs to run: the OPA
// enforcer (from cfg.Intercept.PolicyDir), the port-forwarder and pod execer
// bound to the given agent pod/container, and the audit sink. It is shared by
// runIntercept and the honey intercept-pane subcommand so both build the exact
// same dependency graph. The caller owns the returned sink and must Close it.
func buildInterceptDeps(ctx context.Context, cfg *config.File, restCfg *rest.Config, clientset kubernetes.Interface, namespace, pod, container string) (intercept.Deps, audit.Sink, error) {
	policyDir := ""
	if cfg != nil && cfg.Intercept != nil {
		policyDir = cfg.Intercept.PolicyDir
	}
	enforcer, err := policy.New(ctx, policyDir, nil)
	if err != nil {
		return intercept.Deps{}, nil, fmt.Errorf("intercept: load policy: %w", err)
	}

	sink := gatewayAuditSink(cfg)
	return intercept.Deps{
		PortForwarder: &interceptPortForwarder{cfg: restCfg},
		PodExecer:     &interceptPodExecer{cfg: restCfg, clientset: clientset, namespace: namespace, pod: pod, container: container},
		K8sClient:     clientset,
		Enforcer:      enforcer,
		Sink:          sink,
		LocalRunner:   intercept.DefaultLocalRunner(),
	}, sink, nil
}
