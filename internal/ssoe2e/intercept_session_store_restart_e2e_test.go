//go:build k8s_e2e

package ssoe2e

import (
	"context"
	"crypto/sha256"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

// execCall is one recorded PodExecer.ExecInPod invocation: the cluster,
// namespace, pod, and container the Broker resolved the execer for, plus the
// command it ran.
type execCall struct {
	cluster, namespace, pod, container string
	cmd                                []string
}

// recordingRealExecer is an intercept.BrokerDeps.Execer factory that builds
// REAL PodExecers — via k8sprovider.K8sNativeClient, the exact type honey web
// uses in production — bound to a fixed cluster's rest.Config, and records
// every call any of them makes. The Broker calls Execer fresh for each
// teardown, so one recordingRealExecer backs and observes every call the
// test cares about.
type recordingRealExecer struct {
	cfg *rest.Config
	cs  kubernetes.Interface

	mu    sync.Mutex
	calls []execCall
}

// Execer satisfies intercept.BrokerDeps.Execer.
func (r *recordingRealExecer) Execer(cluster, namespace, pod, container string) (intercept.PodExecer, error) {
	return &recordingPodExec{owner: r, cluster: cluster, namespace: namespace, pod: pod, container: container}, nil
}

// snapshot returns a copy of every call recorded so far.
func (r *recordingRealExecer) snapshot() []execCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]execCall(nil), r.calls...)
}

// recordingPodExec is one PodExecer bound to a specific pod/container. It
// records its own ExecInPod invocation on its owner and then actually runs
// the command for real against the cluster.
type recordingPodExec struct {
	owner                              *recordingRealExecer
	cluster, namespace, pod, container string
}

// ExecInPod records the call, then execs cmd for real via a
// k8sprovider.K8sNativeClient (non-interactive, no terminal-size queue).
func (e *recordingPodExec) ExecInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	e.owner.mu.Lock()
	e.owner.calls = append(e.owner.calls, execCall{
		cluster: e.cluster, namespace: e.namespace, pod: e.pod, container: e.container,
		cmd: append([]string(nil), cmd...),
	})
	e.owner.mu.Unlock()

	client := &k8sprovider.K8sNativeClient{
		Config:    e.owner.cfg,
		Clientset: e.owner.cs,
		Namespace: e.namespace,
		PodName:   e.pod,
		Container: e.container,
	}
	return client.ExecInPod(ctx, cmd, stdin, stdout, stderr, false, nil)
}

// createInterceptStoreProbePod creates a namespace (deleted on t.Cleanup) and
// a single-container pod, then waits for it to become Running. It stands in
// for a target application pod: this test never deploys an ephemeral
// interception agent (it exercises the store/janitor teardown path, not
// Authorize's deploy path — that path is already covered by
// internal/intercept's own e2e and by TestSSOE2E_BrokeredIntercept's RBAC
// split in this package), so the SIGTERM a reap sends lands directly on this
// container's PID 1.
//
// The container's PID 1 is a busybox ash shell that explicitly traps SIGTERM
// and exits 0 — a bare "sleep 3600" would NOT do: the Linux kernel silently
// drops a signal sent to PID 1 of its own pid namespace unless PID 1 has
// installed a handler for it (the well-known "PID 1 problem"), so
// "kill -TERM 1" against a raw sleep process would be a no-op and this test
// would hang forever waiting for a restart that never comes. Trapping TERM
// mirrors how a real, well-behaved interception agent shuts down.
func createInterceptStoreProbePod(t *testing.T, cs kubernetes.Interface, ns, pod, container string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = cs.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	_, err = cs.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: pod, Namespace: ns},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    container,
				Image:   "busybox:1.36",
				Command: []string{"sh", "-c", "trap 'exit 0' TERM; while true; do sleep 1; done"},
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p, gerr := cs.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
		return gerr == nil && p.Status.Phase == corev1.PodRunning
	}, 90*time.Second, 2*time.Second, "probe pod did not become Running")
}

// TestSSOE2E_InterceptSessionStoreRestart proves cross-restart teardown of a
// brokered interception session against a REAL k3s cluster: a session
// persisted by one Broker instance is torn down by a SECOND, independent
// Broker instance opened over the SAME sqlite-backed SessionStore — the
// restart scenario a persistent session_store exists to solve (see
// internal/intercept.TestBroker_ReapAcrossRestart for the equivalent proof
// against a fake clientset; this test proves the same property end-to-end,
// with a real exec landing on a real container).
//
// It models "instance A authorized a session, then honey web crashed" by
// writing a PersistedSession directly to the store (already past its expiry,
// as if the process died before its own janitor tick ever ran), rather than
// calling Authorize and deploying an ephemeral agent — that deploy path is
// already exercised elsewhere (see the doc comment on
// createInterceptStoreProbePod). "Instance B" — standing in for honey web
// after the restart — is a fresh Broker with its own execer and no knowledge
// of instance A, constructed over the same sqlite file. Its janitor must
// reap the persisted-expired session: the row must disappear from the store,
// the real SIGTERM exec must land on the exact pod/container/cluster the
// session recorded (proven via a recording wrapper around the real execer,
// since the probe container's own restart could otherwise race a check that
// happens too early), and the probe container's RestartCount confirms the
// signal's real-world effect end to end.
func TestSSOE2E_InterceptSessionStoreRestart(t *testing.T) {
	adminRest, adminCS := startK3s(t)

	const (
		ns        = "intercept-store-restart"
		podName   = "session-store-probe"
		container = "main"
	)
	createInterceptStoreProbePod(t, adminCS, ns, podName, container)

	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "sessions.db")
	store, err := intercept.NewSQLStore(ctx, "sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// "Instance A authorized a session, then honey web died": a session
	// already past its expiry. TokenHash is a fixture, not a real delivered
	// agent token — reapExpired never checks it (only Stop/StopByToken do),
	// and it is never logged.
	tokenHash := sha256.Sum256([]byte("e2e-fixture-not-a-real-agent-token"))
	now := time.Now().UTC()
	sess := intercept.PersistedSession{
		ID:         "sess-restart-e2e",
		Actor:      "alice",
		Cluster:    clusterName,
		Namespace:  ns,
		Pod:        podName,
		Container:  container,
		Modes:      []string{"egress"},
		AgentImage: "n/a",
		TokenHash:  tokenHash[:],
		StartedAt:  now.Add(-2 * time.Hour),
		ExpiresAt:  now.Add(-time.Hour),
	}
	require.NoError(t, store.Save(ctx, sess))

	// "Instance B": a brand-new Broker, wired to a real execer, over the
	// SAME store file. It knows nothing about instance A.
	execer := &recordingRealExecer{cfg: adminRest, cs: adminCS}
	broker := intercept.NewBroker(intercept.BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return adminCS, nil },
		Execer:     execer.Execer,
		SessionTTL: time.Hour,
		Store:      store,
	})

	janitorCtx, cancelJanitor := context.WithCancel(ctx)
	done := broker.StartJanitor(janitorCtx)
	t.Cleanup(func() {
		cancelJanitor()
		<-done // deterministic: the janitor goroutine has returned before goleak runs
	})

	// require.Eventually may invoke this closure from a goroutine other than
	// the test's own, so failures are signaled by the return value alone
	// (never require/t.Fatal here) — a transient Get error just isn't "gone
	// yet" and the janitor will have another store.List to retry against.
	require.Eventually(t, func() bool {
		_, ok, gerr := store.Get(ctx, sess.ID)
		return gerr == nil && !ok
	}, 90*time.Second, 2*time.Second, "the restarted broker's janitor did not reap the persisted-expired session")

	calls := execer.snapshot()
	require.Len(t, calls, 1, "expected exactly one teardown exec")
	require.Equal(t, ns, calls[0].namespace)
	require.Equal(t, podName, calls[0].pod)
	require.Equal(t, container, calls[0].container)
	require.Contains(t, calls[0].cmd, "kill -TERM 1 2>/dev/null || true",
		"the reap must SIGTERM the agent's PID 1")

	// The SIGTERM's real effect: the probe container's trap exits 0, and the
	// pod's default restart policy brings the container back up.
	// RestartCount is monotonic, so this assertion cannot flake the way
	// "container is currently not Running" could (checked too early or too
	// late relative to the restart).
	require.Eventually(t, func() bool {
		p, gerr := adminCS.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		if gerr != nil {
			return false
		}
		for _, st := range p.Status.ContainerStatuses {
			if st.Name == container && st.RestartCount >= 1 {
				return true
			}
		}
		return false
	}, 60*time.Second, 2*time.Second, "probe container did not restart after the reap's SIGTERM")
}
