package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/policy"
)

// interceptFakeForwarder is a test double for intercept.PortForwarder: it hands
// back a synthetic local address and records every stop it runs so a test can
// assert teardown closed both port-forwards.
type interceptFakeForwarder struct {
	mu      sync.Mutex
	stopped []int
}

func (f *interceptFakeForwarder) Forward(_ context.Context, _, _, _ string, remotePort int) (string, func(), error) {
	return fmt.Sprintf("127.0.0.1:%d", remotePort), func() {
		f.mu.Lock()
		f.stopped = append(f.stopped, remotePort)
		f.mu.Unlock()
	}, nil
}

func (f *interceptFakeForwarder) stoppedPorts() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.stopped...)
}

// interceptTokenExecer is a test double for intercept.PodExecer: it drains (and
// discards) whatever token is streamed to it and succeeds, so the session's
// token-delivery step proceeds without a real pod.
type interceptTokenExecer struct{}

func (interceptTokenExecer) ExecInPod(_ context.Context, _ []string, stdin io.Reader, _, _ io.Writer) error {
	if stdin != nil {
		_, _ = io.Copy(io.Discard, stdin)
	}
	return nil
}

// interceptEchoRunner stands in for the data-plane runner (mogate local.Run): it
// records the config it was handed (to prove the PTY bridge wired it), echoes
// stdin back to stdout (proving the WS<->PTY bridge), and records every window
// size it receives on ResizeCh (proving resize forwarding). It returns when the
// browser stdin closes and the resize channel is closed or the ctx is cancelled,
// leaving no goroutine behind.
type interceptEchoRunner struct {
	mu      sync.Mutex
	cfg     local.Config
	resizes []local.Winsize
}

func (e *interceptEchoRunner) Run(ctx context.Context, cfg local.Config, _ []string) error {
	e.mu.Lock()
	e.cfg = cfg
	e.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case ws, ok := <-cfg.ResizeCh:
				if !ok {
					return
				}
				e.mu.Lock()
				e.resizes = append(e.resizes, ws)
				e.mu.Unlock()
			}
		}
	}()

	if cfg.Stdin != nil && cfg.Stdout != nil {
		_, _ = io.Copy(cfg.Stdout, cfg.Stdin)
	}
	wg.Wait()
	return ctx.Err()
}

func (e *interceptEchoRunner) config() local.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

func (e *interceptEchoRunner) gotResizes() []local.Winsize {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]local.Winsize(nil), e.resizes...)
}

// interceptAllowEnforcer builds an OPA enforcer that allows the intercept action,
// so the direct Session's own gate passes in a test.
func interceptAllowEnforcer(t *testing.T) *policy.Enforcer {
	t.Helper()
	src := `package honey
default allow := false
allow if input.action == "intercept"`
	enf, err := policy.NewFromSource(context.Background(), "intercept.rego", src)
	require.NoError(t, err)
	return enf
}

// startInterceptEphemeralFlipper mimics the kubelet: once the Session applies its
// ephemeral container, it flips the pod status to running so waitEphemeralRunning
// proceeds. The returned stop joins the goroutine so goleak stays clean.
func startInterceptEphemeralFlipper(cs *fake.Clientset, ns, pod string) func() {
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

// podRecord returns a k8s pod host record whose meta carries the namespace, pod
// name, and kube context the handler reads.
func podRecord() hosts.Record {
	return hosts.Record{
		Provider: "k8s",
		Name:     "target",
		Meta: map[string]string{
			"kind":         "pod",
			"namespace":    "apps",
			"pod_name":     "target",
			"kube_context": "prod",
		},
	}
}

// interceptTestConfig is a minimal config that enables intercept with an agent
// image, which buildInterceptOptions requires.
func interceptTestConfig() *config.File {
	return &config.File{
		Intercept: &config.InterceptConfig{
			Enabled:    true,
			AgentImage: "registry.example/agent:1",
		},
	}
}

// TestInterceptPaneRequestFromHello_CarriesFullOptionsSet proves the resume
// path's payload builder carries the same fields buildInterceptOptions sets
// on the fallback path (Container/EnvInclude/EnvExclude/Actor) — a prior
// version silently dropped these, which meant the wrong container got
// injected on multi-container pods, env-mode filters were ignored, and audit
// events had no actor whenever a multiplexer was present.
func TestInterceptPaneRequestFromHello_CarriesFullOptionsSet(t *testing.T) {
	// Table-driven over EnvInclude-only and EnvExclude-only so both filters
	// are actually exercised with a non-empty value, not just empty-to-empty.
	cases := []struct {
		name       string
		envInclude []string
		envExclude []string
	}{
		{name: "env_include", envInclude: []string{"DATABASE_URL", "API_KEY"}},
		{name: "env_exclude", envExclude: []string{"SECRET_KEY"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hello := wsInterceptHello{
				Record:     podRecord(),
				Modes:      []string{"egress", "env"},
				Command:    []string{"/bin/sh"},
				Container:  "sidecar",
				EnvInclude: tc.envInclude,
				EnvExclude: tc.envExclude,
				Cols:       100,
				Rows:       40,
			}
			req := interceptPaneRequestFromHello(hello.Record, hello, "alice")
			require.Equal(t, "sidecar", req.Container)
			require.Equal(t, tc.envInclude, req.EnvInclude)
			require.Equal(t, tc.envExclude, req.EnvExclude)
			require.Equal(t, "alice", req.Actor)

			// The four fields must also survive the actual base64(JSON) argv
			// round-trip the pane decodes.
			raw, err := json.Marshal(req)
			require.NoError(t, err)
			var decoded InterceptPaneRequest
			require.NoError(t, json.Unmarshal(raw, &decoded))
			require.Equal(t, req.Container, decoded.Container)
			require.Equal(t, req.EnvInclude, decoded.EnvInclude)
			require.Equal(t, req.EnvExclude, decoded.EnvExclude)
			require.Equal(t, req.Actor, decoded.Actor)
		})
	}
}

func TestHandleWebIntercept_RejectsNonPodRecord(t *testing.T) {
	var factoryCalls int
	factory := func(hosts.Record, intercept.Options, intercept.LocalRunner) (*intercept.Session, error) {
		factoryCalls++
		return nil, nil
	}
	s := newTestServer(t, Options{Config: interceptTestConfig(), InterceptSessionFactory: factory})
	ts := httptest.NewServer(s.router)
	t.Cleanup(ts.Close)

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/ws/intercept"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// A plain (non-pod) record: the handler must reject it before any session.
	hello := wsInterceptHello{
		Record: hosts.Record{Provider: "local", Name: "vm", PrimaryIP: "10.0.0.1"},
		Modes:  []string{"egress"},
		Cols:   80,
		Rows:   24,
	}
	require.NoError(t, conn.WriteJSON(hello))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	mt, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, mt)
	require.Contains(t, string(payload), "kubernetes pod")
	require.Equal(t, 0, factoryCalls, "no session may be built for a non-pod record")
}

func TestHandleWebIntercept_BridgesStreamsAndTearsDownOnClose(t *testing.T) {
	// This test exercises the in-process fallback (factory + bridgeInterceptWS)
	// via an injected fake session factory; force ptyMuxAvailable() false so a
	// tmux/zellij present on the test host doesn't divert it into the resume
	// path, which the standalone InterceptPane* tests cover instead.
	t.Setenv("PATH", t.TempDir())

	cs := fake.NewSimpleClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "apps"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	})
	fwd := &interceptFakeForwarder{}
	enf := interceptAllowEnforcer(t)
	sink := &captureSink{}

	// A dummy injector file so the Session uses it verbatim instead of extracting
	// the embedded library (the echo runner never actually injects).
	injector := filepath.Join(t.TempDir(), "libhoney-injector.test")
	require.NoError(t, os.WriteFile(injector, []byte("x"), 0o600))

	var capturedOpts intercept.Options
	factory := func(_ hosts.Record, opts intercept.Options, runner intercept.LocalRunner) (*intercept.Session, error) {
		opts.InjectorLib = injector
		opts.InjectorLibRosetta = injector
		capturedOpts = opts
		deps := intercept.Deps{
			PortForwarder: fwd,
			PodExecer:     interceptTokenExecer{},
			K8sClient:     cs,
			Enforcer:      enf,
			Sink:          sink,
			LocalRunner:   runner,
		}
		return intercept.New(deps, opts), nil
	}

	echo := &interceptEchoRunner{}
	s := newTestServer(t, Options{Config: interceptTestConfig(), InterceptSessionFactory: factory})
	s.interceptInnerRunner = echo // the PTY bridge delegates to the echo runner

	stopFlip := startInterceptEphemeralFlipper(cs, "apps", "target")
	defer stopFlip()

	ts := httptest.NewServer(s.router)
	t.Cleanup(ts.Close)

	// Snapshot goroutines now (server + flipper started); only the handler's
	// per-connection goroutines are checked for leaks after the client leaves.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/ws/intercept"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	hello := wsInterceptHello{
		Record: podRecord(),
		Modes:  []string{"egress"},
		Cols:   100,
		Rows:   40,
	}
	require.NoError(t, conn.WriteJSON(hello))

	// stdin -> injected child -> stdout is echoed straight back over the WS.
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("id\r")))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	mt, echoed, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, mt)
	require.Equal(t, "id\r", string(echoed))

	// The live session is registered exactly once, with its metadata.
	require.Eventually(t, func() bool {
		views, _ := s.webIntercepts.list(context.Background())
		return len(views) == 1 && views[0].Pod == "target" && views[0].Namespace == "apps"
	}, 3*time.Second, 10*time.Millisecond, "the live session must be listed in the registry")

	// The PTY bridge forced a pseudo-terminal and built Options from the hello.
	require.True(t, echo.config().Pty, "the browser bridge must run the child on a PTY")
	require.Equal(t, "apps", capturedOpts.Namespace)
	require.Equal(t, "target", capturedOpts.Pod)
	require.Equal(t, "prod", capturedOpts.Cluster)
	require.Equal(t, []string{"/bin/sh"}, capturedOpts.Command)
	require.Equal(t, local.Modes{Egress: true}, capturedOpts.Modes)

	// A resize frame reaches the runner's ResizeCh (as a clamped local.Winsize).
	require.NoError(t, conn.WriteJSON(wsResize{Type: "resize", Cols: 90, Rows: 30}))
	require.Eventually(t, func() bool {
		for _, ws := range echo.gotResizes() {
			if ws.Cols == 90 && ws.Rows == 30 {
				return true
			}
		}
		return false
	}, 3*time.Second, 5*time.Millisecond, "resize must be forwarded to the runner's ResizeCh")

	// Closing the WS cancels the session ctx: teardown stops both port-forwards.
	require.NoError(t, conn.Close())
	require.Eventually(t, func() bool {
		stopped := fwd.stoppedPorts()
		return contains(stopped, 30000) && contains(stopped, 30001)
	}, 5*time.Second, 10*time.Millisecond, "WS close must tear the session down (both forwards stopped)")

	// WS close also deregisters the session from the registry.
	require.Eventually(t, func() bool {
		views, _ := s.webIntercepts.list(context.Background())
		return len(views) == 0
	}, 5*time.Second, 10*time.Millisecond, "WS close must remove the registry entry")

	// A stop audit event was emitted with a cancellation reason.
	require.Eventually(t, func() bool {
		for _, e := range sink.all() {
			if e.Action == "intercept_stop" {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond, "teardown must audit an intercept_stop event")
}

func contains(xs []int, want int) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestHandleWebIntercept_SamePodRejected proves a second interception into a pod
// that already has an active one is rejected before any agent is deployed, and
// the first session stays.
func TestHandleWebIntercept_SamePodRejected(t *testing.T) {
	// Cap/same-pod rejection is admit()'s job, which only the fallback path
	// calls (the resume path's own cap check is a later task); force
	// ptyMuxAvailable() false so a tmux/zellij present on the test host
	// doesn't bypass admit() here.
	t.Setenv("PATH", t.TempDir())

	var factoryCalls int32
	factory := func(hosts.Record, intercept.Options, intercept.LocalRunner) (*intercept.Session, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return nil, nil
	}
	s := newTestServer(t, Options{Config: interceptTestConfig(), InterceptSessionFactory: factory})

	// A session already holds the target pod (resolved cluster/ns/pod from
	// podRecord: prod/apps/target).
	preID, err := s.webIntercepts.admit(context.Background(), intercept.Options{
		Cluster: "prod", Namespace: "apps", Pod: "target", Actor: "bob", Modes: local.Modes{Egress: true},
	}, func() {})
	require.NoError(t, err)

	ts := httptest.NewServer(s.router)
	t.Cleanup(ts.Close)

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/ws/intercept"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(wsInterceptHello{Record: podRecord(), Modes: []string{"egress"}, Cols: 80, Rows: 24}))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(payload), "already has an active interception")
	require.Equal(t, int32(0), atomic.LoadInt32(&factoryCalls), "no agent may be deployed for a rejected same-pod start")

	views, err := s.webIntercepts.list(context.Background())
	require.NoError(t, err)
	require.Len(t, views, 1, "the pre-existing session is untouched")
	require.Equal(t, preID, views[0].ID)
}

// TestHandleWebIntercept_CapRejected proves that with the cap reached, a start on
// a different pod is rejected before any agent is deployed.
func TestHandleWebIntercept_CapRejected(t *testing.T) {
	// Same reasoning as TestHandleWebIntercept_SamePodRejected: the cap check
	// lives in admit(), which only the fallback path calls.
	t.Setenv("PATH", t.TempDir())

	var factoryCalls int32
	factory := func(hosts.Record, intercept.Options, intercept.LocalRunner) (*intercept.Session, error) {
		atomic.AddInt32(&factoryCalls, 1)
		return nil, nil
	}
	cfg := interceptTestConfig()
	cfg.Intercept.MaxSessions = 1
	s := newTestServer(t, Options{Config: cfg, InterceptSessionFactory: factory})

	// One active session on a different pod fills the cap of 1.
	_, err := s.webIntercepts.admit(context.Background(), intercept.Options{
		Cluster: "prod", Namespace: "apps", Pod: "other", Actor: "bob", Modes: local.Modes{Egress: true},
	}, func() {})
	require.NoError(t, err)

	ts := httptest.NewServer(s.router)
	t.Cleanup(ts.Close)

	wsURL := strings.Replace(ts.URL, "http", "ws", 1) + "/ws/intercept"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.WriteJSON(wsInterceptHello{Record: podRecord(), Modes: []string{"egress"}, Cols: 80, Rows: 24}))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, payload, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(payload), "max concurrent sessions (1)")
	require.Equal(t, int32(0), atomic.LoadInt32(&factoryCalls))
}
