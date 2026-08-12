package intercept

import (
	"context"
	"errors"

	"github.com/shareed2k/honey/internal/policy"
)

// errGateDenied is returned when an interception is not permitted. Its message
// is intentionally generic and carries no policy, claim, or token detail.
var errGateDenied = errors.New("intercept: denied by policy")

// GateInput carries the attributes the OPA intercept policy authorizes against.
type GateInput struct {
	// Actor is the authenticated subject requesting the interception.
	Actor string
	// Cluster is the target Kubernetes cluster name.
	Cluster string
	// Namespace is the target pod's namespace.
	Namespace string
	// Pod is the target pod name.
	Pod string
	// Container is the target container within the pod.
	Container string
	// Mode lists the requested interception modes (for example egress,
	// incoming, files).
	Mode []string
	// AgentImage is the operator-configured interception agent image.
	AgentImage string
}

// gate evaluates the OPA intercept policy for in and fails closed: a nil
// enforcer, an evaluation error, or a non-allow decision each yield a non-nil
// error. Only an explicit allow returns nil. The returned error is generic and
// carries no policy or token detail.
func gate(ctx context.Context, enf *policy.Enforcer, in GateInput) error {
	if enf == nil {
		return errGateDenied
	}
	input := map[string]any{
		"action":      "intercept",
		"actor":       in.Actor,
		"cluster":     in.Cluster,
		"namespace":   in.Namespace,
		"pod":         in.Pod,
		"container":   in.Container,
		"mode":        in.Mode,
		"agent_image": in.AgentImage,
	}
	dec, err := enf.Evaluate(ctx, input)
	if err != nil || !dec.Allow {
		return errGateDenied
	}
	return nil
}
