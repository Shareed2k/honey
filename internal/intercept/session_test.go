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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

// newTargetlessHarness bundles a fully wired set of fakes for a targetless
// Session. Unlike newHarness, the fake clientset starts with no pods seeded:
// the standalone agent pod is created by Run itself (provisionTargetless),
// mirroring how the CLI generates opts.Pod before Deps is built.
func newTargetlessHarness(t *testing.T, allow bool, runErr error) *harness {
	t.Helper()
	log := &eventLog{}
	cs := fake.NewSimpleClientset()
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
		Pod:        "mogate-test1234",
		Cluster:    "prod",
		AgentImage: "registry.example/agent:1",
		Modes:      local.Modes{Egress: true},
		UDP:        false,
		Command:    []string{"curl", "http://svc"},
		Actor:      "roman",
		Targetless: true,
	}
	return &harness{deps: deps, opts: opts, cs: cs, fwd: fwd, runner: runner, sink: sink, log: log}
}

// startPodRunningFlipper mimics the kubelet for a standalone targetless agent
// pod: once the Session has created it (createAgentPod), it records the
// observation and flips the pod to Running with its agent container Ready, so
// waitPodRunning proceeds. The returned stop joins the goroutine so goleak
// stays clean.
func startPodRunningFlipper(cs *fake.Clientset, ns, pod string, log *eventLog) func() {
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
			if err != nil {
				continue
			}
			log.record("pod-created")
			p.Status.Phase = corev1.PodRunning
			p.Status.ContainerStatuses = []corev1.ContainerStatus{
				{Name: AgentContainerName, Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			}
			_, _ = cs.CoreV1().Pods(ns).UpdateStatus(context.Background(), p, metav1.UpdateOptions{})
			return
		}
	}()
	return func() {
		close(stopCh)
		<-done
	}
}

// eventWaitTimeout bounds how long waitForEvent polls the log for an event
// before failing the test. Every caller uses the same window, so it lives here
// rather than as a per-call parameter.
const eventWaitTimeout = 3 * time.Second

func waitForEvent(t *testing.T, log *eventLog, event string) {
	t.Helper()
	deadline := time.Now().Add(eventWaitTimeout)
	for time.Now().Before(deadline) {
		for _, e := range log.snapshot() {
			if e == event {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("event %q not observed within %s; log=%v", event, eventWaitTimeout, log.snapshot())
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

	waitForEvent(t, h.log, "run")
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

// TestSession_envThreadingToLocalConfig proves the env mode wiring flows end to
// end through a targeted Session: Modes.Env and the include/exclude key filters
// reach the LocalRunner's local.Config, and the deployed ephemeral container
// carries the /proc-read capabilities the agent needs to read the target's
// environ. It carries only key NAMES, never values, so nothing sensitive is
// asserted or logged.
func TestSession_envThreadingToLocalConfig(t *testing.T) {
	defer goleak.VerifyNone(t)

	h := newHarness(t, true, nil)
	h.opts.Modes = local.Modes{Egress: true, Env: true}
	h.opts.EnvInclude = []string{"DATABASE_URL", "FEATURE_FLAG"}
	h.opts.EnvExclude = nil
	stopFlip := startEphemeralFlipper(h.cs, h.opts.Namespace, h.opts.Pod, h.log)
	defer stopFlip()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- New(h.deps, h.opts).Run(ctx) }()

	// Wait until the ephemeral container is applied, then assert it carries the
	// /proc-read caps for env mode on top of NET_ADMIN.
	waitForEvent(t, h.log, "ephemeral-applied")
	p, err := h.cs.CoreV1().Pods(h.opts.Namespace).Get(context.Background(), h.opts.Pod, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, p.Spec.EphemeralContainers, 1)
	add := p.Spec.EphemeralContainers[0].SecurityContext.Capabilities.Add
	assert.Equal(t, []corev1.Capability{capNetAdmin, capSysPtrace, capDacReadSearch}, add)

	// Wait until the LocalRunner is actually invoked before tearing down, so the
	// config it captured is populated by the time it returns.
	waitForEvent(t, h.log, "run")
	cancel()
	var runErr error
	select {
	case runErr = <-res:
	case <-time.After(shutdownGrace + 2*time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	assert.ErrorIs(t, runErr, context.Canceled)

	// The LocalRunner received the env mode and the include filter verbatim
	// (cfg is captured at the start of Run, so it is set by the time Run
	// returns). Only key names are asserted — never values.
	cfg := h.runner.config()
	assert.True(t, cfg.Modes.Env, "Modes.Env must reach local.Config")
	assert.Equal(t, []string{"DATABASE_URL", "FEATURE_FLAG"}, cfg.EnvInclude)
	assert.Empty(t, cfg.EnvExclude)
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

	waitForEvent(t, h.log, "run")

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

// TestSession_targetless_happyPathLifecycleAndTeardown covers the targetless
// runtime end to end: Run must create a standalone agent Pod named opts.Pod
// (not add an ephemeral container to any pre-existing pod), wait for it to
// run, deliver the token, forward the egress port only (no control port, so
// no incoming capture), and delete the standalone Pod on teardown.
func TestSession_targetless_happyPathLifecycleAndTeardown(t *testing.T) {
	defer goleak.VerifyNone(t)

	h := newTargetlessHarness(t, true, nil)
	stopFlip := startPodRunningFlipper(h.cs, h.opts.Namespace, h.opts.Pod, h.log)
	defer stopFlip()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := make(chan error, 1)
	go func() { res <- New(h.deps, h.opts).Run(ctx) }()

	waitForEvent(t, h.log, "run")

	// While the session is live, the standalone pod exists with exactly one
	// container (the agent) and no ephemeral containers at all — targetless
	// never touches the ephemeral-container API.
	p, err := h.cs.CoreV1().Pods(h.opts.Namespace).Get(context.Background(), h.opts.Pod, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, p.Spec.EphemeralContainers, "targetless must not add an ephemeral container")
	require.Len(t, p.Spec.Containers, 1)
	assert.Equal(t, AgentContainerName, p.Spec.Containers[0].Name)

	cancel()
	var runErr error
	select {
	case runErr = <-res:
	case <-time.After(shutdownGrace + 2*time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	assert.ErrorIs(t, runErr, context.Canceled)

	// Ordered lifecycle: pod create -> token -> egress forward -> run. No
	// control port is ever forwarded in targetless mode.
	assertOrder(t, h.log.snapshot(), "pod-created", "token-delivered", fmt.Sprintf("forward:%d", agentEgressRemotePort), "run")
	assert.NotContains(t, h.fwd.forwarded(), agentControlRemotePort)

	cfg := h.runner.config()
	assert.Empty(t, cfg.ControlAddr, "targetless has no control port-forward")
	assert.Equal(t, fmt.Sprintf("127.0.0.1:%d", agentEgressRemotePort), cfg.EgressAddr)
	assert.Equal(t, local.Modes{Egress: true}, cfg.Modes)

	// Teardown stopped the egress forward (and only it).
	stopped := h.fwd.stoppedPorts()
	assert.Contains(t, stopped, agentEgressRemotePort)
	assert.NotContains(t, stopped, agentControlRemotePort)

	// Teardown deleted the standalone agent pod outright — unlike the targeted
	// path, there is no pod left behind with a lingering ephemeral container.
	_, getErr := h.cs.CoreV1().Pods(h.opts.Namespace).Get(context.Background(), h.opts.Pod, metav1.GetOptions{})
	assert.Truef(t, k8serrors.IsNotFound(getErr), "standalone agent pod must be deleted on teardown")

	require.Len(t, h.sink.events, 2)
	assert.Equal(t, actionInterceptStart, h.sink.events[0].Action)
	assert.Equal(t, actionInterceptStop, h.sink.events[1].Action)
}

// TestSession_targetless_teardownOnRunnerError guards that the standalone
// agent Pod is deleted even when the local runner fails mid-session, not just
// on a clean cancellation.
func TestSession_targetless_teardownOnRunnerError(t *testing.T) {
	defer goleak.VerifyNone(t)

	boom := errors.New("runner boom")
	h := newTargetlessHarness(t, true, boom)
	stopFlip := startPodRunningFlipper(h.cs, h.opts.Namespace, h.opts.Pod, h.log)
	defer stopFlip()

	err := New(h.deps, h.opts).Run(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, boom)

	stopped := h.fwd.stoppedPorts()
	assert.Contains(t, stopped, agentEgressRemotePort)
	assert.NotContains(t, stopped, agentControlRemotePort)

	_, getErr := h.cs.CoreV1().Pods(h.opts.Namespace).Get(context.Background(), h.opts.Pod, metav1.GetOptions{})
	assert.Truef(t, k8serrors.IsNotFound(getErr), "standalone agent pod must be deleted on teardown even after a runner error")

	require.Len(t, h.sink.events, 2)
	assert.Equal(t, actionInterceptStop, h.sink.events[1].Action)
	assert.Equal(t, "runner boom", h.sink.events[1].Extra["reason"])
}
