package intercept

import (
	"context"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/audit"
)

// interceptSource is the audit Source tag for interception events.
const interceptSource = "cli"

// Audit action names for the interception lifecycle.
const (
	actionInterceptStart = "intercept_start"
	actionInterceptStop  = "intercept_stop"
)

// Event is the audit payload for one interception. It deliberately carries no
// session token: tokens are never audited.
type Event struct {
	// Actor is the authenticated subject that ran the interception.
	Actor string
	// Cluster is the target Kubernetes cluster name (used as the audit target).
	Cluster string
	// Namespace is the target pod's namespace.
	Namespace string
	// Pod is the target pod name.
	Pod string
	// Container is the target container within the pod.
	Container string
	// Mode lists the interception modes that were requested.
	Mode []string
	// AgentImage is the operator-configured interception agent image.
	AgentImage string
	// Reason is the stop reason (recorded on stop events only).
	Reason string
	// Duration is how long the interception ran (recorded on stop events only).
	Duration time.Duration
}

// auditStart records the start of an interception (action intercept_start).
func auditStart(ctx context.Context, sink audit.Sink, ev Event) {
	emitIntercept(ctx, sink, actionInterceptStart, ev, false)
}

// auditStop records the end of an interception (action intercept_stop),
// including its duration and stop reason.
func auditStop(ctx context.Context, sink audit.Sink, ev Event) {
	emitIntercept(ctx, sink, actionInterceptStop, ev, true)
}

// emitIntercept builds and logs the audit event. It is a no-op for a nil sink,
// and never records a token because Event carries none.
func emitIntercept(ctx context.Context, sink audit.Sink, action string, ev Event, withOutcome bool) {
	if sink == nil {
		return
	}
	extra := map[string]string{
		"namespace": ev.Namespace,
		"pod":       ev.Pod,
		"container": ev.Container,
		"mode":      strings.Join(ev.Mode, ","),
		"image":     ev.AgentImage,
	}
	if withOutcome {
		extra["duration"] = ev.Duration.String()
		if ev.Reason != "" {
			extra["reason"] = ev.Reason
		}
	}
	_ = sink.Log(ctx, audit.Event{
		Actor:  ev.Actor,
		Source: interceptSource,
		Action: action,
		Target: ev.Cluster,
		Extra:  extra,
	})
}
