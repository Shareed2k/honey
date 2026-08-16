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
	// Subject is the verified SSO subject (id_token sub claim). Empty on the
	// direct (client-side) path.
	Subject string
	// Email is the verified SSO email/username claim. Empty on the direct path.
	Email string
	// Groups are the verified SSO groups claim. Empty on the direct path.
	Groups []string
	// Claims is the full set of verified id_token claims, made available to the
	// policy so operators can authorize against any claim. Populated only on the
	// server-brokered path; nil on the direct path.
	Claims map[string]any
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
	// Server-brokered path: expose the verified identity and its full claim set
	// so operators can write group/email/arbitrary-claim policies. Added only
	// when present so the direct path's input — and existing policies — are
	// unchanged.
	if in.Subject != "" {
		input["subject"] = in.Subject
	}
	if in.Email != "" {
		input["email"] = in.Email
	}
	if len(in.Groups) > 0 {
		input["groups"] = in.Groups
	}
	if len(in.Claims) > 0 {
		input["claims"] = in.Claims
	}
	dec, err := enf.Evaluate(ctx, input)
	if err != nil || !dec.Allow {
		return errGateDenied
	}
	return nil
}
