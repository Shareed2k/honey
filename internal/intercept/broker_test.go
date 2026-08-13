package intercept

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/shareed2k/mogate/pkg/local"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/shareed2k/honey/internal/policy"
)

// NOTE: do NOT declare a package-level TestMain here — internal/intercept
// already has one in e2e_test.go (//go:build k8s_e2e); a second TestMain
// collides under `-tags k8s_e2e`. Use per-test `defer goleak.VerifyNone(t)`
// on the goroutine-spawning tests instead.

// runningPodClient returns a fake clientset for the single pod every
// single-session test in this file targets ("prod-ns"/"api-7d9f"), whose pod
// immediately reports every applied ephemeral container as running, so
// waitEphemeralRunning returns fast without needing to track the exact
// applied container name.
func runningPodClient() *fake.Clientset {
	return runningPodsClient("prod-ns", "api-7d9f")
}

// runningPodsClient is runningPodClient generalized to one or more
// independent pods. Each pod gets its own reactor-driven status update, so
// concurrent callers targeting different pods don't race on a shared pod
// object's non-atomic get-modify-update ephemeral-container apply (a
// limitation of the fake clientset, not of the code under test).
func runningPodsClient(ns string, pods ...string) *fake.Clientset {
	objs := make([]runtime.Object, 0, len(pods))
	for _, pod := range pods {
		objs = append(objs, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: pod}})
	}
	cs := fake.NewSimpleClientset(objs...)
	cs.PrependReactor("get", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		getAction, ok := a.(k8stesting.GetAction)
		if !ok {
			return false, nil, nil
		}
		obj, err := cs.Tracker().Get(a.GetResource(), ns, getAction.GetName())
		if err != nil {
			return true, nil, err
		}
		p, ok := obj.(*corev1.Pod)
		if !ok {
			return true, obj, nil
		}
		statuses := make([]corev1.ContainerStatus, 0, len(p.Spec.EphemeralContainers))
		for _, ec := range p.Spec.EphemeralContainers {
			statuses = append(statuses, corev1.ContainerStatus{
				Name:  ec.Name,
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			})
		}
		p.Status.EphemeralContainerStatuses = statuses
		return true, p, nil
	})
	return cs
}

// recordingExecer records every ExecInPod invocation. It is safe for
// concurrent use so tests may share one instance across goroutines.
type recordingExecer struct {
	mu    sync.Mutex
	calls [][]string
}

func (e *recordingExecer) ExecInPod(_ context.Context, cmd []string, _ io.Reader, _, _ io.Writer) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, cmd)
	return nil
}

// failingExecer records calls like recordingExecer but always fails, so a
// test can force a post-deploy step (e.g. deliverToken) to error and observe
// whether Authorize still signals the already-deployed agent to exit.
type failingExecer struct{ recordingExecer }

func (e *failingExecer) ExecInPod(ctx context.Context, cmd []string, in io.Reader, out, errw io.Writer) error {
	_ = e.recordingExecer.ExecInPod(ctx, cmd, in, out, errw)
	return errors.New("boom: exec failed")
}

func newBrokerWith(t *testing.T, enf *policy.Enforcer, exec *recordingExecer, ttl time.Duration, clock func() time.Time, store SessionStore) *Broker {
	t.Helper()
	cs := runningPodClient()
	return NewBroker(BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return cs, nil },
		Execer:     func(_, _, _, _ string) (PodExecer, error) { return exec, nil },
		Enforcer:   enf,
		Sink:       nil,
		SessionTTL: ttl,
		now:        clock,
		Store:      store,
	})
}

// sessionStoreVariant names one SessionStore constructor under test.
type sessionStoreVariant struct {
	name string
	new  func(t *testing.T) SessionStore
}

// sessionStoreVariants is every SessionStore backend the Broker's behavioral
// tests must pass against, proving Authorize/Stop/reapExpired are store-
// agnostic.
func sessionStoreVariants() []sessionStoreVariant {
	return []sessionStoreVariant{
		{name: "mem", new: func(*testing.T) SessionStore { return newMemStore() }},
		{name: "sqlite", new: func(t *testing.T) SessionStore {
			t.Helper()
			s, err := newSQLStore(context.Background(), "sqlite3", "file::memory:?cache=shared")
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			return s
		}},
	}
}

func TestBroker_Authorize_DeploysAndReturnsSession(t *testing.T) {
	for _, v := range sessionStoreVariants() {
		t.Run(v.name, func(t *testing.T) {
			exec := &recordingExecer{}
			b := newBrokerWith(t, newAllowPolicy(t), exec, time.Hour, time.Now, v.new(t))
			sess, err := b.Authorize(context.Background(), AuthorizeRequest{
				Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f",
				Container: "web", Modes: local.Modes{Egress: true}, AgentImage: "img:v1",
				Claims: map[string]any{"groups": []any{"developers"}},
			})
			require.NoError(t, err)
			require.NotEmpty(t, sess.ID)
			require.NotEmpty(t, sess.Token)
			require.Equal(t, agentControlRemotePort, sess.ControlPort)
			require.Equal(t, agentEgressRemotePort, sess.EgressPort)
			require.Len(t, exec.calls, 1) // token delivered
		})
	}
}

func TestBroker_Authorize_GateDeny(t *testing.T) {
	b := newBrokerWith(t, nil /*nil enforcer ⇒ fail-closed*/, &recordingExecer{}, time.Hour, time.Now, newMemStore())
	_, err := b.Authorize(context.Background(), AuthorizeRequest{Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f"})
	require.ErrorIs(t, err, errGateDenied)
}

// TestBroker_Authorize_PostDeployFailureSignalsAgent covers the orphaned-agent
// case: applyEphemeral succeeds (the privileged, NET_ADMIN agent now exists
// and Kubernetes cannot remove it), but a later step — here deliverToken —
// fails before the session is registered. Authorize must still best-effort
// SIGTERM the agent so it tears down its network redirects, since neither
// Stop nor reapExpired can ever find a session that never made it into the
// registry.
func TestBroker_Authorize_PostDeployFailureSignalsAgent(t *testing.T) {
	cs := runningPodClient()
	exec := &failingExecer{}
	b := NewBroker(BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return cs, nil },
		Execer:     func(_, _, _, _ string) (PodExecer, error) { return exec, nil },
		Enforcer:   newAllowPolicy(t),
		SessionTTL: time.Hour,
	})

	_, err := b.Authorize(context.Background(), AuthorizeRequest{
		Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f",
		Container: "web", Modes: local.Modes{Egress: true}, AgentImage: "img:v1",
	})
	require.Error(t, err) // deliverToken failed

	// The agent was deployed and reachable, so deliverToken's attempt plus the
	// cleanup-defer's SIGTERM must both have been recorded.
	require.Len(t, exec.calls, 2)
	require.Contains(t, exec.calls[1], "kill -TERM 1 2>/dev/null || true")

	// The never-registered session must not linger in the store.
	list, err := b.deps.Store.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, list)
}

// failingStore wraps a memStore but makes Save always fail, so a test can
// force the post-deploy persistence step to error and observe whether
// Authorize still signals the already-deployed agent to exit.
type failingStore struct{ *memStore }

func (s *failingStore) Save(context.Context, PersistedSession) error {
	return errors.New("boom: save failed")
}

// TestBroker_Authorize_StoreSaveFailureSignalsAgent covers the same
// orphaned-agent case as TestBroker_Authorize_PostDeployFailureSignalsAgent,
// but for the store.Save step specifically: applyEphemeral and deliverToken
// both succeed, yet persisting the session fails. Authorize must still
// best-effort SIGTERM the agent, and the session must never appear in the
// store, since it never should have been considered registered.
func TestBroker_Authorize_StoreSaveFailureSignalsAgent(t *testing.T) {
	cs := runningPodClient()
	exec := &recordingExecer{}
	store := &failingStore{memStore: newMemStore()}
	b := NewBroker(BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return cs, nil },
		Execer:     func(_, _, _, _ string) (PodExecer, error) { return exec, nil },
		Enforcer:   newAllowPolicy(t),
		SessionTTL: time.Hour,
		Store:      store,
	})

	_, err := b.Authorize(context.Background(), AuthorizeRequest{
		Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f",
		Container: "web", Modes: local.Modes{Egress: true}, AgentImage: "img:v1",
	})
	require.Error(t, err) // store.Save failed

	// deliverToken succeeded (exec call 1); the cleanup-defer's SIGTERM
	// (exec call 2) must still fire because Save failed before the session
	// was ever considered registered.
	require.Len(t, exec.calls, 2)
	require.Contains(t, exec.calls[1], "kill -TERM 1 2>/dev/null || true")

	list, err := store.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestBroker_Stop_SignalsAgentAndDeregisters(t *testing.T) {
	for _, v := range sessionStoreVariants() {
		t.Run(v.name, func(t *testing.T) {
			exec := &recordingExecer{}
			b := newBrokerWith(t, newAllowPolicy(t), exec, time.Hour, time.Now, v.new(t))
			sess, err := b.Authorize(context.Background(), AuthorizeRequest{Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f", Modes: local.Modes{Incoming: true}})
			require.NoError(t, err)
			require.NoError(t, b.Stop(context.Background(), sess.ID, "alice", "completed"))
			// SIGTERM exec recorded (kill -TERM 1).
			require.Contains(t, exec.calls[len(exec.calls)-1], "kill -TERM 1 2>/dev/null || true")
			// Second stop ⇒ unknown session.
			require.Error(t, b.Stop(context.Background(), sess.ID, "alice", "completed"))
		})
	}
}

func TestBroker_Stop_WrongActorDenied(t *testing.T) {
	for _, v := range sessionStoreVariants() {
		t.Run(v.name, func(t *testing.T) {
			b := newBrokerWith(t, newAllowPolicy(t), &recordingExecer{}, time.Hour, time.Now, v.new(t))
			sess, err := b.Authorize(context.Background(), AuthorizeRequest{Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f"})
			require.NoError(t, err)
			require.Error(t, b.Stop(context.Background(), sess.ID, "mallory", "completed"))
		})
	}
}

func TestBroker_Janitor_StopsExpired(t *testing.T) {
	for _, v := range sessionStoreVariants() {
		t.Run(v.name, func(t *testing.T) {
			exec := &recordingExecer{}
			now := time.Unix(1_000_000, 0)
			clock := func() time.Time { return now }
			b := newBrokerWith(t, newAllowPolicy(t), exec, time.Minute, clock, v.new(t))
			sess, err := b.Authorize(context.Background(), AuthorizeRequest{Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f", Modes: local.Modes{Incoming: true}})
			require.NoError(t, err)
			now = now.Add(2 * time.Minute)                                                // past ExpiresAt
			require.Equal(t, 1, b.reapExpired(context.Background()))                      // reap once, deterministically
			require.Error(t, b.Stop(context.Background(), sess.ID, "alice", "completed")) // gone
		})
	}
}

func TestBroker_StartJanitor_ExitsOnCtxCancel(t *testing.T) {
	defer goleak.VerifyNone(t) // the janitor goroutine must exit on ctx cancel
	b := newBrokerWith(t, newAllowPolicy(t), &recordingExecer{}, time.Hour, time.Now, newMemStore())
	ctx, cancel := context.WithCancel(context.Background())
	done := b.StartJanitor(ctx)
	cancel()
	<-done // deterministic: the janitor goroutine has returned before goleak runs
}

// TestBroker_ConcurrentAuthorizeStop drives many goroutines, each authorizing
// and then stopping its own session against a single Broker, to exercise the
// session registry's locking under -race. Each goroutine targets its own pod:
// the fake clientset's UpdateEphemeralContainers is a non-atomic
// get-modify-update (unlike the real API server's resourceVersion-checked
// one), so concurrent Authorize calls against one shared pod object would
// race and lose each other's ephemeral container — a fake-clientset
// limitation unrelated to the Broker's own registry locking under test.
func TestBroker_ConcurrentAuthorizeStop(t *testing.T) {
	const n = 20
	pods := make([]string, n)
	for i := range pods {
		pods[i] = fmt.Sprintf("api-%d", i)
	}
	cs := runningPodsClient("prod-ns", pods...)
	exec := &recordingExecer{}
	b := NewBroker(BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return cs, nil },
		Execer:     func(_, _, _, _ string) (PodExecer, error) { return exec, nil },
		Enforcer:   newAllowPolicy(t),
		SessionTTL: time.Hour,
	})

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			actor := fmt.Sprintf("actor-%d", i)
			sess, err := b.Authorize(context.Background(), AuthorizeRequest{
				Actor: actor, Cluster: "prod", Namespace: "prod-ns", Pod: pods[i],
				Container: "web", Modes: local.Modes{Egress: true}, AgentImage: "img:v1",
			})
			require.NoError(t, err)
			require.NoError(t, b.Stop(context.Background(), sess.ID, actor, "completed"))
		}(i)
	}
	wg.Wait()
}

// TestBroker_StopByToken_Ownership proves StopByToken authenticates by the
// per-session agent token rather than actor identity: the correct token tears
// the session down, a wrong token is rejected and leaves the session in
// place, and an unknown id is always an error.
func TestBroker_StopByToken_Ownership(t *testing.T) {
	exec := &recordingExecer{}
	b := newBrokerWith(t, newAllowPolicy(t), exec, time.Hour, time.Now, newMemStore())
	sess, err := b.Authorize(context.Background(), AuthorizeRequest{
		Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f",
		Modes: local.Modes{Incoming: true},
	})
	require.NoError(t, err)

	// Unknown id ⇒ error, regardless of token.
	require.Error(t, b.StopByToken(context.Background(), "no-such-session", sess.Token, "completed"))

	// Wrong token ⇒ error, session remains.
	require.Error(t, b.StopByToken(context.Background(), sess.ID, "wrong-token", "completed"))
	_, ok, err := b.deps.Store.Get(context.Background(), sess.ID)
	require.NoError(t, err)
	require.True(t, ok)

	// Correct token ⇒ SIGTERM recorded, session gone.
	require.NoError(t, b.StopByToken(context.Background(), sess.ID, sess.Token, "completed"))
	require.Contains(t, exec.calls[len(exec.calls)-1], "kill -TERM 1 2>/dev/null || true")
	_, ok, err = b.deps.Store.Get(context.Background(), sess.ID)
	require.NoError(t, err)
	require.False(t, ok)
}

// TestBroker_ReapAcrossRestart proves sessions survive a honey web restart:
// broker A authorizes a session against a sqlite-backed store, then broker B
// — a fresh Broker standing in for the process after a restart, with its own
// execer and no knowledge of broker A — is constructed over the SAME store
// handle. Once the session is past its expiry, broker B's janitor reaps it
// and signals the agent, proving teardown does not depend on the process that
// authorized the session.
func TestBroker_ReapAcrossRestart(t *testing.T) {
	ctx := context.Background()
	store, err := newSQLStore(ctx, "sqlite3", "file::memory:?cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	cs := runningPodClient()
	now := time.Unix(1_000_000, 0)
	clock := func() time.Time { return now }

	execA := &recordingExecer{}
	brokerA := NewBroker(BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return cs, nil },
		Execer:     func(_, _, _, _ string) (PodExecer, error) { return execA, nil },
		Enforcer:   newAllowPolicy(t),
		SessionTTL: time.Minute,
		now:        clock,
		Store:      store,
	})
	sess, err := brokerA.Authorize(ctx, AuthorizeRequest{
		Actor: "alice", Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f",
		Container: "web", Modes: local.Modes{Egress: true}, AgentImage: "img:v1",
	})
	require.NoError(t, err)

	// Simulate honey web restarting: brokerB knows nothing of brokerA (a
	// distinct execer, a distinct Broker value) and is wired to the same
	// persisted store.
	execB := &recordingExecer{}
	brokerB := NewBroker(BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return cs, nil },
		Execer:     func(_, _, _, _ string) (PodExecer, error) { return execB, nil },
		Enforcer:   newAllowPolicy(t),
		SessionTTL: time.Minute,
		now:        clock,
		Store:      store,
	})

	now = now.Add(2 * time.Minute) // past ExpiresAt
	require.Equal(t, 1, brokerB.reapExpired(ctx))
	require.Contains(t, execB.calls[len(execB.calls)-1], "kill -TERM 1 2>/dev/null || true")
	require.Len(t, execA.calls, 1) // only the original token delivery; brokerA was never touched by the reap

	_, ok, err := store.Get(ctx, sess.ID)
	require.NoError(t, err)
	require.False(t, ok)
}
