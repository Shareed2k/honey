// Package interceptwire builds the real (non-test) intercept.Deps a session
// needs to run against an actual Kubernetes cluster: an OPA enforcer, a
// port-forwarder and pod execer bound to the agent pod/container, and an
// audit sink. It exists so both internal/cli and internal/engine — which
// cannot import internal/cli — build the exact same dependency graph without
// duplicating it.
package interceptwire

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

// BuildDeps wires the intercept.Deps a session needs to run: the OPA enforcer
// (from cfg.Intercept.PolicyDir), the port-forwarder and pod execer bound to
// the given agent pod/container, and the audit sink. It is shared by honey
// intercept, the honey intercept-pane subcommand, and internal/engine so all
// three build the exact same dependency graph. The caller owns the returned
// sink and must Close it.
func BuildDeps(ctx context.Context, cfg *config.File, restCfg *rest.Config, clientset kubernetes.Interface, namespace, pod, container string) (intercept.Deps, audit.Sink, error) {
	policyDir := ""
	if cfg != nil && cfg.Intercept != nil {
		policyDir = cfg.Intercept.PolicyDir
	}
	enforcer, err := policy.New(ctx, policyDir, nil)
	if err != nil {
		return intercept.Deps{}, nil, fmt.Errorf("intercept: load policy: %w", err)
	}

	sink := auditSink(cfg)
	return intercept.Deps{
		PortForwarder: &PortForwarder{Cfg: restCfg},
		PodExecer:     &PodExecer{Cfg: restCfg, Clientset: clientset, Namespace: namespace, Pod: pod, Container: container},
		K8sClient:     clientset,
		Enforcer:      enforcer,
		Sink:          sink,
		LocalRunner:   intercept.DefaultLocalRunner(),
	}, sink, nil
}

// auditSink builds the audit sink from config. It mirrors internal/cli's
// gatewayAuditSink body: interceptwire needs its own small copy rather than
// importing internal/cli for it (which would cycle back to this package, and
// internal/cli's sink is shared by non-intercept callers too, so it stays
// there).
func auditSink(cfg *config.File) audit.Sink {
	if cfg != nil && cfg.Audit.Enabled {
		path := cfg.Audit.EffectivePath()
		if s, err := audit.NewFileSink(path); err == nil {
			return s
		}
	}
	return audit.NewNoopSink()
}
