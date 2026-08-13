package intercept

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
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
	// Execer returns a PodExecer bound to a specific ephemeral container. It is
	// called fresh whenever a session is torn down (Stop, StopByToken, or the
	// janitor's reap), since a PodExecer is never persisted.
	Execer func(cluster, namespace, pod, container string) (PodExecer, error)
	// Enforcer authorizes each interception; a nil enforcer fails closed.
	Enforcer *policy.Enforcer
	// Sink receives intercept_start/intercept_stop events; may be nil.
	Sink audit.Sink
	// SessionTTL bounds an authorized session's lifetime; the janitor tears down
	// any session past it. Non-positive ⇒ defaultSessionTTL.
	SessionTTL time.Duration
	// Store persists sessions so they survive a honey web restart: the janitor
	// can reap a session authorized by a prior process, and Stop/StopByToken can
	// find one. Nil ⇒ an in-memory store (today's behavior: sessions do not
	// survive a restart).
	Store SessionStore
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

// Broker deploys and tears down interception agents for authenticated,
// authorized callers. One honey web process owns one Broker. All session
// state lives in deps.Store, so the Broker itself holds no mutable state and
// needs no lock: concurrency safety is the store's responsibility.
type Broker struct {
	deps BrokerDeps
	ttl  time.Duration
	now  func() time.Time
}

// NewBroker constructs a Broker from its dependencies. A nil deps.Store gets
// an in-memory store.
func NewBroker(deps BrokerDeps) *Broker {
	ttl := deps.SessionTTL
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	now := deps.now
	if now == nil {
		now = time.Now
	}
	if deps.Store == nil {
		deps.Store = newMemStore()
	}
	return &Broker{deps: deps, ttl: ttl, now: now}
}

// Authorize gates the request (with the caller's full claims), deploys the
// agent as an ephemeral container using honey's cluster credentials, delivers a
// per-session token to the agent, persists the session (keyed by the sha256 of
// the token, never the plaintext), and audits the start. Fail-closed: a gate
// denial short-circuits before any deploy and is not audited. It never logs
// the token or any claim material.
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
	// is persisted, this best-effort SIGTERM signals the agent (PID 1 of its
	// container) to exit so it removes its network redirects — otherwise it
	// would be orphaned forever: neither Stop nor reapExpired can find a session
	// that never made it into the store.
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
	sum := sha256.Sum256([]byte(token))
	ps := PersistedSession{
		ID: id, Actor: req.Actor, Cluster: req.Cluster,
		Namespace: req.Namespace, Pod: req.Pod, Container: agentName,
		Modes: modeStrings(req.Modes), AgentImage: req.AgentImage,
		TokenHash: sum[:], StartedAt: nowT, ExpiresAt: nowT.Add(b.ttl),
	}
	if err := b.deps.Store.Save(ctx, ps); err != nil {
		// The defer above SIGTERMs the just-deployed agent: the session never
		// made it into the store, so nothing else will ever find it.
		return nil, fmt.Errorf("intercept: save session: %w", err)
	}
	registered = true

	auditStart(ctx, b.deps.Sink, Event{
		Actor: ps.Actor, Cluster: ps.Cluster, Namespace: ps.Namespace,
		Pod: ps.Pod, Container: ps.Container, Mode: ps.Modes, AgentImage: ps.AgentImage,
	})

	return &BrokeredSession{
		ID: ps.ID, Token: token,
		ControlPort: agentControlRemotePort, EgressPort: agentEgressRemotePort,
		ExpiresAt: ps.ExpiresAt,
	}, nil
}

// Stop signals the agent to exit (best-effort SIGTERM via exec, so it removes
// its network redirects), deregisters the session, and audits the stop. actor
// must own the session. An unknown id is an error.
func (b *Broker) Stop(ctx context.Context, id, actor, reason string) error {
	sess, ok, err := b.deps.Store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("intercept: get session: %w", err)
	}
	if !ok {
		return errors.New("intercept: unknown session")
	}
	if sess.Actor != actor {
		return errors.New("intercept: session not owned by actor")
	}
	return b.teardown(ctx, sess, reason)
}

// StopByToken signals the agent to exit and deregisters the session,
// authenticating the caller by the per-session agent token instead of an
// actor identity. This lets a local session tear itself down even after the
// SSO id_token that originally authorized it has expired. The comparison
// against the stored hash is constant-time; the token itself is never logged
// and the error is deliberately generic so it leaks nothing about why a
// token was rejected.
func (b *Broker) StopByToken(ctx context.Context, id, token, reason string) error {
	sess, ok, err := b.deps.Store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("intercept: get session: %w", err)
	}
	if !ok {
		return errors.New("intercept: unknown session")
	}
	sum := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(sum[:], sess.TokenHash) != 1 {
		return errors.New("intercept: invalid session token")
	}
	return b.teardown(ctx, sess, reason)
}

// teardown signals sess's agent to exit, removes sess from the store, and
// audits the stop. The execer is rebuilt on demand from the session's
// cluster/namespace/pod/container: a PodExecer is never persisted.
func (b *Broker) teardown(ctx context.Context, sess PersistedSession, reason string) error {
	execer, err := b.deps.Execer(sess.Cluster, sess.Namespace, sess.Pod, sess.Container)
	if err != nil {
		return fmt.Errorf("intercept: build execer: %w", err)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), agentStopGrace)
	_ = execer.ExecInPod(stopCtx, []string{"sh", "-c", "kill -TERM 1 2>/dev/null || true"}, nil, io.Discard, io.Discard)
	cancel()

	if err := b.deps.Store.Delete(ctx, sess.ID); err != nil {
		return fmt.Errorf("intercept: delete session: %w", err)
	}

	auditStop(ctx, b.deps.Sink, Event{
		Actor: sess.Actor, Cluster: sess.Cluster, Namespace: sess.Namespace,
		Pod: sess.Pod, Container: sess.Container, Mode: sess.Modes, AgentImage: sess.AgentImage,
		Reason: reason, Duration: b.now().Sub(sess.StartedAt),
	})
	return nil
}

// reapExpired stops every session past its expiry and returns how many it
// reaped. It is the janitor's unit of work, exposed to the package for a
// deterministic test. A session whose teardown fails (e.g. a transient store
// or cluster error) is left in place for the next tick to retry.
func (b *Broker) reapExpired(ctx context.Context) int {
	now := b.now()
	list, err := b.deps.Store.List(ctx)
	if err != nil {
		return 0
	}
	count := 0
	for _, sess := range list {
		if !now.After(sess.ExpiresAt) {
			continue
		}
		if err := b.teardown(ctx, sess, "expired"); err != nil {
			continue
		}
		count++
	}
	return count
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
