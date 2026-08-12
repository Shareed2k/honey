package intercept

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/shareed2k/mogate/pkg/local"
	"k8s.io/client-go/kubernetes"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/policy"
)

// janitorInterval is how often the TTL janitor scans for expired sessions.
const janitorInterval = 30 * time.Second

// defaultSessionTTL bounds a brokered session when none is configured.
const defaultSessionTTL = time.Hour

// BrokerDeps are the server-side dependencies the Broker needs to deploy and
// tear down interception agents on a caller's behalf. Clientset and Execer are
// resolved per cluster from honey's own (service-account) credentials.
type BrokerDeps struct {
	// Clientset returns honey's Kubernetes client for a cluster.
	Clientset func(cluster string) (kubernetes.Interface, error)
	// Execer returns a PodExecer bound to a specific ephemeral container.
	Execer func(cluster, namespace, pod, container string) (PodExecer, error)
	// Enforcer authorizes each interception; a nil enforcer fails closed.
	Enforcer *policy.Enforcer
	// Sink receives intercept_start/intercept_stop events; may be nil.
	Sink audit.Sink
	// SessionTTL bounds an authorized session's lifetime; the janitor tears down
	// any session past it. Non-positive ⇒ defaultSessionTTL.
	SessionTTL time.Duration
	// now is the clock (injectable for tests); nil ⇒ time.Now.
	now func() time.Time
}

// AuthorizeRequest is one server-brokered interception request.
type AuthorizeRequest struct {
	// Actor is the authenticated subject requesting the interception.
	Actor string
	// Subject is the verified SSO subject (id_token sub claim).
	Subject string
	// Email is the verified SSO email/username claim.
	Email string
	// Groups are the verified SSO groups claim.
	Groups []string
	// Claims is the full set of verified id_token claims, made available to the
	// gate policy.
	Claims map[string]any

	// Cluster is the target Kubernetes cluster name.
	Cluster string
	// Namespace is the target pod's namespace.
	Namespace string
	// Pod is the target pod name.
	Pod string
	// Container is the target container within the pod.
	Container string
	// Modes selects which interception capabilities are active.
	Modes local.Modes
	// UDP includes the UDP tunnels alongside TCP.
	UDP bool
	// Target is the local application address incoming steal traffic is
	// forwarded to (interpreted by the caller's local session, not the broker).
	Target string
	// AgentImage is the operator-configured interception agent image.
	AgentImage string
}

// BrokeredSession is the handle returned to an authorized caller: the opaque
// session id, the per-session agent token, the two agent ports to port-forward
// to, and the absolute expiry.
type BrokeredSession struct {
	// ID is the opaque session identifier used to Stop the session.
	ID string
	// Token is the per-session agent token; never logged.
	Token string
	// ControlPort is the in-agent control port to port-forward to.
	ControlPort int
	// EgressPort is the in-agent remote-egress port to port-forward to.
	EgressPort int
	// ExpiresAt is the absolute time the janitor will reap this session.
	ExpiresAt time.Time
}

// activeSession is the Broker's internal record for a live interception.
type activeSession struct {
	id        string
	actor     string
	cluster   string
	namespace string
	pod       string
	container string // the ephemeral agent container name
	modes     []string
	image     string
	execer    PodExecer
	startedAt time.Time
	expiresAt time.Time
}

// Broker deploys and tears down interception agents for authenticated,
// authorized callers. One honey web process owns one Broker.
type Broker struct {
	deps BrokerDeps
	ttl  time.Duration
	now  func() time.Time

	mu       sync.Mutex
	sessions map[string]*activeSession
}

// NewBroker constructs a Broker from its dependencies.
func NewBroker(deps BrokerDeps) *Broker {
	ttl := deps.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	now := deps.now
	if now == nil {
		now = time.Now
	}
	return &Broker{deps: deps, ttl: ttl, now: now, sessions: make(map[string]*activeSession)}
}

// Authorize gates the request (with the caller's full claims), deploys the
// agent as an ephemeral container using honey's cluster credentials, delivers a
// per-session token to the agent, registers the session, and audits the start.
// Fail-closed: a gate denial short-circuits before any deploy and is not
// audited. It never logs the token or any claim material.
func (b *Broker) Authorize(ctx context.Context, req AuthorizeRequest) (*BrokeredSession, error) {
	if gerr := gate(ctx, b.deps.Enforcer, GateInput{
		Actor: req.Actor, Cluster: req.Cluster, Namespace: req.Namespace,
		Pod: req.Pod, Container: req.Container, Mode: modeStrings(req.Modes),
		AgentImage: req.AgentImage, Subject: req.Subject, Email: req.Email,
		Groups: req.Groups, Claims: req.Claims,
	}); gerr != nil {
		return nil, gerr
	}

	client, err := b.deps.Clientset(req.Cluster)
	if err != nil {
		return nil, fmt.Errorf("intercept: resolve cluster client: %w", err)
	}
	token, err := mintToken()
	if err != nil {
		return nil, err
	}
	id, err := mintToken() // opaque, unguessable session id (reuses the CSPRNG helper; not the agent token)
	if err != nil {
		return nil, err
	}
	agentName, err := agentContainerName()
	if err != nil {
		return nil, fmt.Errorf("intercept: name agent: %w", err)
	}
	execer, err := b.deps.Execer(req.Cluster, req.Namespace, req.Pod, agentName)
	if err != nil {
		return nil, fmt.Errorf("intercept: build execer: %w", err)
	}

	ec := ephemeralContainer(agentName, req.AgentImage, req.Container, agentArgs(req.UDP))
	if err := applyEphemeral(ctx, client, req.Namespace, req.Pod, ec); err != nil {
		return nil, fmt.Errorf("intercept: deploy agent: %w", err)
	}
	// From here the agent exists and Kubernetes cannot remove it (an ephemeral
	// container cannot be deleted). If any later step fails before the session
	// is registered, this best-effort SIGTERM signals the agent (PID 1 of its
	// container) to exit so it removes its network redirects — otherwise it
	// would be orphaned forever: neither Stop nor reapExpired can find a session
	// that never made it into the registry.
	registered := false
	defer func() {
		if registered {
			return
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), agentStopGrace)
		_ = execer.ExecInPod(stopCtx, []string{"sh", "-c", "kill -TERM 1 2>/dev/null || true"}, nil, io.Discard, io.Discard)
		cancel()
	}()
	if err := waitEphemeralRunning(ctx, client, req.Namespace, req.Pod, agentName, ephemeralReadyTimeout); err != nil {
		return nil, fmt.Errorf("intercept: agent not ready: %w", err)
	}
	if err := deliverToken(ctx, execer, token); err != nil {
		return nil, fmt.Errorf("intercept: deliver token: %w", err)
	}

	nowT := b.now()
	sess := &activeSession{
		id: id, actor: req.Actor, cluster: req.Cluster,
		namespace: req.Namespace, pod: req.Pod, container: agentName,
		modes: modeStrings(req.Modes), image: req.AgentImage,
		execer: execer, startedAt: nowT, expiresAt: nowT.Add(b.ttl),
	}
	b.mu.Lock()
	b.sessions[sess.id] = sess
	b.mu.Unlock()
	registered = true

	auditStart(ctx, b.deps.Sink, Event{
		Actor: sess.actor, Cluster: sess.cluster, Namespace: sess.namespace,
		Pod: sess.pod, Container: sess.container, Mode: sess.modes, AgentImage: sess.image,
	})

	return &BrokeredSession{
		ID: sess.id, Token: token,
		ControlPort: agentControlRemotePort, EgressPort: agentEgressRemotePort,
		ExpiresAt: sess.expiresAt,
	}, nil
}

// Stop signals the agent to exit (best-effort SIGTERM via exec, so it removes
// its network redirects), audits the stop, and deregisters the session. actor
// must own the session. An unknown id is an error.
func (b *Broker) Stop(ctx context.Context, id, actor, reason string) error {
	b.mu.Lock()
	sess, ok := b.sessions[id]
	if ok && sess.actor == actor {
		delete(b.sessions, id)
	}
	b.mu.Unlock()
	if !ok {
		return errors.New("intercept: unknown session")
	}
	if sess.actor != actor {
		return errors.New("intercept: session not owned by actor")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), agentStopGrace)
	defer cancel()
	_ = sess.execer.ExecInPod(stopCtx, []string{"sh", "-c", "kill -TERM 1 2>/dev/null || true"}, nil, io.Discard, io.Discard)

	auditStop(ctx, b.deps.Sink, Event{
		Actor: sess.actor, Cluster: sess.cluster, Namespace: sess.namespace,
		Pod: sess.pod, Container: sess.container, Mode: sess.modes, AgentImage: sess.image,
		Reason: reason, Duration: b.now().Sub(sess.startedAt),
	})
	return nil
}

// reapExpired stops every session past its expiry and returns how many it
// reaped. It is the janitor's unit of work, exposed to the package for a
// deterministic test.
func (b *Broker) reapExpired(ctx context.Context) int {
	now := b.now()
	b.mu.Lock()
	var expired []*activeSession
	for id, s := range b.sessions {
		if now.After(s.expiresAt) {
			expired = append(expired, s)
			delete(b.sessions, id)
		}
	}
	b.mu.Unlock()
	for _, s := range expired {
		stopCtx, cancel := context.WithTimeout(context.Background(), agentStopGrace)
		_ = s.execer.ExecInPod(stopCtx, []string{"sh", "-c", "kill -TERM 1 2>/dev/null || true"}, nil, io.Discard, io.Discard)
		cancel()
		auditStop(ctx, b.deps.Sink, Event{
			Actor: s.actor, Cluster: s.cluster, Namespace: s.namespace,
			Pod: s.pod, Container: s.container, Mode: s.modes, AgentImage: s.image,
			Reason: "expired", Duration: now.Sub(s.startedAt),
		})
	}
	return len(expired)
}

// StartJanitor runs reapExpired on a ticker until ctx is cancelled. It is a
// single goroutine with a clear exit; goleak-safe. The returned channel is
// closed once the janitor goroutine has returned, so callers (notably tests)
// can deterministically wait for its exit instead of guessing with a sleep.
func (b *Broker) StartJanitor(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(janitorInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				b.reapExpired(ctx)
			}
		}
	}()
	return done
}
