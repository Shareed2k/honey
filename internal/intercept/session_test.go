package intercept

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shareed2k/honey/internal/policy"

	"github.com/shareed2k/mogate/pkg/local"
)

// eventLog is a concurrency-safe ordered record of lifecycle events the fakes
// emit, so tests can assert the orchestration order.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) record(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// fakeForwarder records every port-forward it opens and every stop it runs.
type fakeForwarder struct {
	mu       sync.Mutex
	forwards []int
	stopped  []int
	log      *eventLog
}

func (f *fakeForwarder) Forward(_ context.Context, _, _, _ string, remotePort int) (string, func(), error) {
	f.mu.Lock()
	f.forwards = append(f.forwards, remotePort)
	f.mu.Unlock()
	f.log.record(fmt.Sprintf("forward:%d", remotePort))
	stop := func() {
		f.mu.Lock()
		f.stopped = append(f.stopped, remotePort)
		f.mu.Unlock()
		f.log.record(fmt.Sprintf("stop:%d", remotePort))
	}
	return fmt.Sprintf("127.0.0.1:%d", remotePort), stop, nil
}

func (f *fakeForwarder) forwarded() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.forwards...)
}

func (f *fakeForwarder) stoppedPorts() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.stopped...)
}

// fakeRunner stands in for the data-plane local session: it records the config
// and command it received and, unless configured to fail, blocks until ctx is
// cancelled (as a real injection session does).
type fakeRunner struct {
	mu     sync.Mutex
	cfg    local.Config
	cmd    []string
	called bool
	runErr error
	log    *eventLog
}

func (f *fakeRunner) Run(ctx context.Context, cfg local.Config, cmd []string) error {
	f.mu.Lock()
	f.cfg = cfg
	f.cmd = cmd
	f.called = true
	f.mu.Unlock()
	f.log.record("run")
	if f.runErr != nil {
		return f.runErr
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeRunner) config() local.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg
}

func (f *fakeRunner) command() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cmd
}

func (f *fakeRunner) wasCalled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.called
}

// loggingExecer records that the token was delivered and drains the token from
// stdin (without retaining it).
type loggingExecer struct {
	log *eventLog
	err error
}

func (e *loggingExecer) ExecInPod(_ context.Context, _ []string, stdin io.Reader, _, _ io.Writer) error {
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin)
	}
	e.log.record("token-delivered")
	return e.err
}

// harness bundles a fully wired set of fakes for a Session.
type harness struct {
	deps   Deps
	opts   Options
	cs     *fake.Clientset
	fwd    *fakeForwarder
	runner *fakeRunner
	sink   *recordingSink
	log    *eventLog
}

func buildEnforcer(t *testing.T, allow bool) *policy.Enforcer {
	t.Helper()
	src := `package honey
default allow := false`
	if allow {
		src = `package honey
default allow := false
allow if input.action == "intercept"`
	}
	enf, err := policy.NewFromSource(context.Background(), "intercept.rego", src)
	require.NoError(t, err)
	return enf
}

func newHarness(t *testing.T, allow bool, runErr error) *harness {
	t.Helper()
	log := &eventLog{}
	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "apps"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	})
	fwd := &fakeForwarder{log: log}
	runner := &fakeRunner{log: log, runErr: runErr}
	sink := &recordingSink{}
	deps := Deps{
		PortForwarder: fwd,
		PodExecer:     &loggingExecer{log: log},
		K8sClient:     cs,
		Enforcer:      buildEnforcer(t, allow),
		Sink:          sink,
		LocalRunner:   runner,
	}
	opts := Options{
		Namespace:  "apps",
		Pod:        "target",
		Container:  "app",
		Cluster:    "prod",
		AgentImage: "registry.example/agent:1",
		Target:     "127.0.0.1:9000",
		Modes:      local.Modes{Egress: true, Incoming: true, Files: true},
		UDP:        true,
		Command:    []string{"curl", "http://svc"},
		Actor:      "roman",
	}
	return &harness{deps: deps, opts: opts, cs: cs, fwd: fwd, runner: runner, sink: sink, log: log}
}

// startEphemeralFlipper mimics the kubelet: once the Session has applied the
// ephemeral container it records the observation and flips the pod status to
// running so waitEphemeralRunning proceeds. The returned stop joins the
// goroutine so goleak stays clean.
func startEphemeralFlipper(cs *fake.Clientset, ns, pod string, log *eventLog) func() {
	stopCh := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
			}
			p, err := cs.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
			if err != nil || len(p.Spec.EphemeralContainers) == 0 {
				continue
			}
			log.record("ephemeral-applied")
			statuses := make([]corev1.ContainerStatus, 0, len(p.Spec.EphemeralContainers))
			for _, ec := range p.Spec.EphemeralContainers {
				statuses = append(statuses, corev1.ContainerStatus{
					Name:  ec.Name,
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				})
			}
			p.Status.EphemeralContainerStatuses = statuses
			_, _ = cs.CoreV1().Pods(ns).UpdateStatus(context.Background(), p, metav1.UpdateOptions{})
			return
		}
	}()
	return func() {
		close(stopCh)
		<-done
	}
}

func waitForEvent(t *testing.T, log *eventLog, event string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, e := range log.snapshot() {
			if e == event {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("event %q not observed within %s; log=%v", event, timeout, log.snapshot())
}

func indexOf(events []string, want string) int {
	for i, e := range events {
		if e == want {
			return i
		}
	}
	return -1
}

func assertOrder(t *testing.T, events []string, ordered ...string) {
	t.Helper()
	last := -1
	for _, want := range ordered {
		idx := indexOf(events, want)
		require.GreaterOrEqualf(t, idx, 0, "expected event %q in log %v", want, events)
		assert.Greaterf(t, idx, last, "event %q out of order in log %v", want, events)
		last = idx
	}
}

func TestSession_gateDenyShortCircuits(t *testing.T) {
	defer goleak.VerifyNone(t)

	h := newHarness(t, false, nil)

	err := New(h.deps, h.opts).Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, errGateDenied)

	// A denial short-circuits before any deploy, token, forward, or run.
	assert.Empty(t, h.log.snapshot(), "gate deny must short-circuit before any lifecycle step")
	assert.False(t, h.runner.wasCalled(), "LocalRunner must not run on a denied request")
	assert.Empty(t, h.fwd.forwarded(), "no port-forward on a denied request")
	assert.Empty(t, h.sink.events, "a denial short-circuits before the start audit, so nothing is emitted")

	p, err := h.cs.CoreV1().Pods("apps").Get(context.Background(), "target", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, p.Spec.EphemeralContainers, "no ephemeral container on a denied request")
}

func TestSession_happyPathLifecycleAndTeardown(t *testing.T) {
	defer goleak.VerifyNone(t)

	h := newHarness(t, true, nil)
	stopFlip := startEphemeralFlipper(h.cs, "apps", "target", h.log)
	defer stopFlip()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- New(h.deps, h.opts).Run(ctx) }()

	waitForEvent(t, h.log, "run", 3*time.Second)
	cancel()

	var runErr error
	select {
	case runErr = <-res:
	case <-time.After(shutdownGrace + 2*time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	assert.ErrorIs(t, runErr, context.Canceled)

	// Ordered lifecycle: deploy -> token -> forwards -> run.
	assertOrder(t, h.log.snapshot(),
		"ephemeral-applied", "token-delivered", "forward:30000", "forward:30001", "run")

	// The LocalRunner received the expected config.
	cfg := h.runner.config()
	assert.Equal(t, "127.0.0.1:30000", cfg.ControlAddr)
	assert.Equal(t, "127.0.0.1:30001", cfg.EgressAddr)
	assert.Equal(t, "127.0.0.1:9000", cfg.Target)
	assert.True(t, cfg.UDP)
	assert.Equal(t, local.Modes{Egress: true, Incoming: true, Files: true}, cfg.Modes)
	assert.Equal(t, DefaultFileRoot, cfg.Root)
	assert.Equal(t, tokenFileName, filepath.Base(cfg.TokenFile))
	assert.Equal(t, RelaySocketName, filepath.Base(cfg.Socket))
	assert.NotEmpty(t, cfg.InjectorLib)
	assert.Equal(t, h.opts.Command, h.runner.command())

	// Teardown stopped both forwards.
	stopped := h.fwd.stoppedPorts()
	assert.Contains(t, stopped, agentControlRemotePort)
	assert.Contains(t, stopped, agentEgressRemotePort)

	// Teardown removed the session directory.
	dir := filepath.Dir(cfg.Socket)
	_, statErr := os.Stat(dir)
	assert.Truef(t, os.IsNotExist(statErr), "session dir %q must be removed on teardown", dir)

	// Audit: start then stop with a duration and a cancellation reason.
	require.Len(t, h.sink.events, 2)
	assert.Equal(t, actionInterceptStart, h.sink.events[0].Action)
	assert.Equal(t, actionInterceptStop, h.sink.events[1].Action)
	assert.Equal(t, "canceled", h.sink.events[1].Extra["reason"])
	assert.NotEmpty(t, h.sink.events[1].Extra["duration"])
}

func TestSession_teardownOnRunnerError(t *testing.T) {
	defer goleak.VerifyNone(t)

	boom := errors.New("runner boom")
	h := newHarness(t, true, boom)
	stopFlip := startEphemeralFlipper(h.cs, "apps", "target", h.log)
	defer stopFlip()

	err := New(h.deps, h.opts).Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)

	// Even on a runner error both forwards are stopped and the dir is removed.
	stopped := h.fwd.stoppedPorts()
	assert.Contains(t, stopped, agentControlRemotePort)
	assert.Contains(t, stopped, agentEgressRemotePort)

	cfg := h.runner.config()
	dir := filepath.Dir(cfg.Socket)
	_, statErr := os.Stat(dir)
	assert.Truef(t, os.IsNotExist(statErr), "session dir %q must be removed on teardown", dir)

	// The stop audit carries the failure reason.
	require.Len(t, h.sink.events, 2)
	assert.Equal(t, actionInterceptStop, h.sink.events[1].Action)
	assert.Equal(t, "runner boom", h.sink.events[1].Extra["reason"])
}

func TestSession_resolveInjectorOverridePrecedence(t *testing.T) {
	// An explicit InjectorLib takes precedence over the embedded library and is
	// returned verbatim (proving Run would load the operator/test-supplied lib).
	override := filepath.Join(t.TempDir(), "libhoney-injector.test")
	require.NoError(t, os.WriteFile(override, []byte("injector"), 0o600))

	s := New(Deps{}, Options{InjectorLib: override})
	got, err := s.resolveInjector(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, override, got, "an explicit InjectorLib must be used verbatim")

	// A missing override path is a clear error, not a silent fallback.
	sMissing := New(Deps{}, Options{InjectorLib: filepath.Join(t.TempDir(), "does-not-exist")})
	_, err = sMissing.resolveInjector(t.TempDir())
	require.Error(t, err)

	// With no override, resolveInjector falls back to extracting the embedded
	// library into the session dir (a path under that dir, not the override).
	dir := t.TempDir()
	sDefault := New(Deps{}, Options{})
	extracted, err := sDefault.resolveInjector(dir)
	require.NoError(t, err)
	assert.Equal(t, dir, filepath.Dir(extracted), "the embedded library is extracted into the session dir")
	assert.NotEqual(t, override, extracted)
}

func TestSession_ctxCancelDrainsWithinGrace(t *testing.T) {
	defer goleak.VerifyNone(t)

	h := newHarness(t, true, nil)
	stopFlip := startEphemeralFlipper(h.cs, "apps", "target", h.log)
	defer stopFlip()

	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { res <- New(h.deps, h.opts).Run(ctx) }()

	waitForEvent(t, h.log, "run", 3*time.Second)

	cancel()
	start := time.Now()
	select {
	case err := <-res:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(shutdownGrace + 2*time.Second):
		t.Fatal("Run did not return within the grace window")
	}
	assert.Lessf(t, time.Since(start), shutdownGrace,
		"a well-behaved runner must drain well within the %s grace window", shutdownGrace)
}
