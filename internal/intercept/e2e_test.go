//go:build k8s_e2e

// Package intercept k8s_e2e matrix: exercises the FULL honey intercept session
// end to end against a REAL k3s cluster (via testcontainers), the REAL mogate
// agent image, and the REAL mogate injector library — every subtest driven
// through intercept.Session.Run with a real client-go clientset, a real SPDY
// port-forwarder, a real in-pod execer, a real policy.Enforcer, and a capturing
// audit sink.
//
// The data plane (agent image + injector) is built from a fresh checkout of the
// public mogate module during setup: the host injector library via
// `go generate ./internal/protocol` + `make build`, and a linux agent image via
// `docker build`. honey deploys the genuine mogate `kube-agent`, which waits for
// its --token-file natively while honey delivers the token out of band (via
// exec) just after the container starts — its real delivery path, no wrapper.
// The test layers only a thin entrypoint to inject test-timing flags (see
// buildImages); it never waits for or touches the token.
//
// Excluded from the normal `go test` run (and CI) by the k8s_e2e build tag.
// Requires a reachable Docker daemon. The ONLY skips are the environment gates
// (Docker/clone/build unavailable). Every interception assertion is mandatory
// (require). One k3s cluster and one set of images are shared across all
// subtests; each subtest that deploys an agent uses its OWN fresh target pod
// (k8s ephemeral containers cannot be removed once added, and the agent's
// nftables rules persist), so subtests cannot interfere. Run explicitly:
//
//	DOCKER_HOST=unix://$HOME/.colima/emulator_x86/docker.sock \
//	TESTCONTAINERS_RYUK_DISABLED=true \
//	go test -tags k8s_e2e -run TestInterceptE2E ./internal/intercept/ -v -timeout 45m
package intercept

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"go.uber.org/goleak"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8srand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"

	"github.com/shareed2k/mogate/pkg/local"
)

const (
	// e2eK3sImage pins the same k3s image the k8s-proxy matrix uses.
	e2eK3sImage = "rancher/k3s:v1.31.5-k3s1"
	// mogateRepo and mogateTag identify the public data-plane module built here.
	// mogateTag is the release whose agent supports the targeted (nftables),
	// targetless (--no-redirect, egress-only), AND env (OpEnvGet: reads the
	// target's /proc/1/environ) modes. It matches honey's own go.mod mogate
	// version, so the injector library and agent built here speak exactly the
	// wire protocol honey's compiled-in local.Run expects — env mode (the
	// env_overlay subtest) needs an agent that answers OpEnvGet, which landed in
	// v0.1.8.
	mogateRepo = "https://github.com/shareed2k/mogate"
	mogateTag  = "v0.1.8"
	// agentBaseImage is the genuine mogate agent image, built from the module's
	// own (unmodified) Dockerfile. The honey agent images layer only a thin
	// timing-flag entrypoint on top of it (no token wait — kube-agent waits
	// for its --token-file itself).
	agentBaseImage = "mogate-agent-base:e2e"
	// agentImageSteal and agentImageMirror are the two locally-built agent images
	// (steal is the mogate default; mirror sets --mode=mirror + a larger
	// --mirror-claim-window via the wrapper's HONEY_AGENT_EXTRA).
	agentImageSteal  = "honey-intercept-agent:e2e"
	agentImageMirror = "honey-intercept-agent-mirror:e2e"
	// targetImage is the small echo/HTTP+UDP server deployed as the target/egress.
	targetImage = "honey-intercept-target:e2e"
	// appPort is the target application port (matches the agent's --app-port).
	appPort = 8080
	// agentFileContent is baked onto the agent image root and read back by the
	// files subtest to prove remote file redirection.
	agentFileContent = "honey-intercept-file-ok"
	// agentFilePath is where agentFileContent lives on the agent image root.
	agentFilePath = "/honey-intercept-file"
)

// TestMain runs the suite and then asserts no goroutine leaked. Shared
// containers cancel their operation context on cleanup so the Docker client's
// keep-alive goroutine is released before this check runs.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// e2eEnv bundles the shared, build-once/start-once fixtures every subtest reuses.
type e2eEnv struct {
	rest        *rest.Config
	admin       *kubernetes.Clientset
	injectorLib string // host injector library path (DYLD/LD_PRELOAD target)
	clientBin   string // freshly built, non-SIP injected client binary
	egressAddr  string // ClusterIP:port of the shared egress target
	egressDNS   string // cluster DNS name:port of the shared egress target
	egressResp  string // the egress target's echo response
	probePod    string // long-lived in-cluster probe pod (namespace default)
}

func TestInterceptE2E(t *testing.T) {
	requireDocker(t)

	// mogate checkout + host data-plane build (injector library).
	mogateDir := cloneMogate(t)
	injectorLib := buildHostInjector(t, mogateDir)

	// Agent + target images, imported into the k3s node's containerd.
	buildImages(t, mogateDir)

	rc, admin, k3sContainer := startK3s(t)
	importImages(t, k3sContainer)

	// Cluster DNS must be up before the egress_dns/udp subtests resolve a
	// Service name through the pod.
	waitForClusterDNS(t, admin, 2*time.Minute)

	// The injected client: a tiny, freshly-built (non-SIP) libc program so the
	// mogate injector's DYLD/LD_PRELOAD hooks apply (a pure-Go client would use
	// raw syscalls and bypass the shim).
	clientBin := buildInjectedClient(t)

	env := &e2eEnv{rest: rc, admin: admin, injectorLib: injectorLib, clientBin: clientBin}

	// Shared egress target: a passive echo server the injected client reaches
	// THROUGH the target pod (the host cannot route a ClusterIP or resolve a
	// cluster DNS name, so a response proves the traffic egressed from the pod).
	env.egressResp = "egress-remote"
	egressName := "egress-target"
	createEchoDeployment(t, admin, "default", egressName, env.egressResp)
	svcIP := createService(t, admin, "default", egressName, egressName)
	env.egressAddr = net.JoinHostPort(svcIP, strconv.Itoa(appPort))
	env.egressDNS = net.JoinHostPort(egressName+".default.svc.cluster.local", strconv.Itoa(appPort))

	// Long-lived probe pod for the incoming subtests (execs one-shot requests
	// into the target Service from inside the cluster).
	env.probePod = "intercept-probe"
	createProbePod(t, admin, "default", env.probePod)
	waitPodReady(t, admin, "default", env.probePod, 3*time.Minute)

	t.Run("egress_tcp", func(t *testing.T) { testEgressTCP(t, env) })
	t.Run("egress_dns", func(t *testing.T) { testEgressDNS(t, env) })
	t.Run("egress_udp", func(t *testing.T) { testEgressUDP(t, env) })
	t.Run("targetless_egress", func(t *testing.T) { testTargetlessEgress(t, env) })
	t.Run("incoming_steal_tcp", func(t *testing.T) { testIncomingStealTCP(t, env) })
	t.Run("incoming_mirror_tcp", func(t *testing.T) { testIncomingMirrorTCP(t, env) })
	t.Run("incoming_steal_udp", func(t *testing.T) { testIncomingStealUDP(t, env) })
	t.Run("files", func(t *testing.T) { testFiles(t, env) })
	t.Run("env_overlay", func(t *testing.T) { testEnvOverlay(t, env) })
	t.Run("passthrough_baseline", func(t *testing.T) { testPassthroughBaseline(t, env) })
	t.Run("gate_deny", func(t *testing.T) { testGateDeny(t, env) })
	t.Run("audit", func(t *testing.T) { testAudit(t, env) })
	t.Run("teardown", func(t *testing.T) { testTeardown(t, env) })
}

// ---- subtests ----

// testEgressTCP proves the injected client reaches an in-cluster service by
// ClusterIP through the pod's egress relay (the host cannot route a ClusterIP).
func testEgressTCP(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPod(t, env, "target-pod")
	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Egress: true}, "",
		[]string{env.clientBin, "tcp", host(env.egressAddr), strconv.Itoa(appPort), env.egressResp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	requireSessionOK(t, env, ns, pod, err)
}

// testEgressDNS proves the injected client resolves a cluster Service DNS name
// via the pod's remote resolver, then connects through the pod.
func testEgressDNS(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPod(t, env, "target-pod")
	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Egress: true}, "",
		[]string{env.clientBin, "tcp", host(env.egressDNS), strconv.Itoa(appPort), env.egressResp})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	requireSessionOK(t, env, ns, pod, err)
}

// testEgressUDP proves a UDP datagram round-trip through the pod (--udp).
func testEgressUDP(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPod(t, env, "target-pod")
	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Egress: true}, "",
		[]string{env.clientBin, "udp", host(env.egressDNS), strconv.Itoa(appPort), env.egressResp})
	opts.UDP = true

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	requireSessionOK(t, env, ns, pod, err)
}

// testTargetlessEgress proves the targetless (no target pod) path: honey
// deploys its OWN standalone, non-root, no-NET_ADMIN agent Pod — not an
// ephemeral container in a workload pod — and the injected client reaches the
// shared egress target through it, exactly like testEgressTCP but with no
// target pod anywhere in the picture. It also proves the standalone Pod is
// gone once the session tears down.
//
// Unlike every ephemeral-container subtest above, this one never calls
// elevateEphemeralPrivilege (there is no ephemeral container to elevate):
// targetless installs no nftables (--no-redirect), so the agent needs no
// privilege at all. That makes this subtest also the proof of the non-root
// egress path, on a genuinely un-elevated agent Pod.
func testTargetlessEgress(t *testing.T, env *e2eEnv) {
	pod, err := NewAgentPodName()
	require.NoError(t, err)
	ns := "default"

	sink := &captureSink{}
	fwd := &recordingForwarder{cfg: env.rest, clientset: env.admin}
	deps := Deps{
		PortForwarder: fwd,
		// The standalone Pod's single container has a known, fixed name
		// (AgentContainerName) — unlike the targeted path there is no
		// ephemeral container to resolve at exec time, so the execer is told
		// the container directly (mirrors interceptPodExecer's targetless
		// wiring in internal/cli/intercept.go).
		PodExecer:   &agentExecer{cfg: env.rest, clientset: env.admin, namespace: ns, pod: pod, container: AgentContainerName},
		K8sClient:   env.admin,
		Enforcer:    buildAllowEnforcer(t),
		Sink:        sink,
		LocalRunner: DefaultLocalRunner(),
	}
	opts := Options{
		Targetless: true,
		Namespace:  ns,
		Pod:        pod,
		Cluster:    "e2e",
		// The STOCK mogate image straight from the module's own Dockerfile —
		// not the steal/mirror wrapper. Targetless runs the agent non-root and
		// egress-only, and the stock image waits for its token natively.
		AgentImage:  agentBaseImage,
		Modes:       local.Modes{Egress: true},
		Command:     []string{env.clientBin, "tcp", host(env.egressAddr), strconv.Itoa(appPort), env.egressResp},
		Actor:       "e2e",
		InjectorLib: env.injectorLib,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Session.Run deletes the standalone Pod as part of its own teardown
	// BEFORE Run returns (unlike the targeted path's ephemeral container,
	// which nothing ever removes) — so there is no window after Run returns to
	// fetch its logs. Poll diagnostics WHILE the session runs and freeze the
	// last snapshot to print if the session fails.
	stopDiag := pollPodDiagnostics(env, ns, pod)
	err = New(deps, opts).Run(ctx)
	diag := stopDiag()
	if err != nil {
		t.Fatalf("Session.Run failed: %v\n%s", err, diag)
	}

	_, getErr := env.admin.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
	require.True(t, k8serrors.IsNotFound(getErr), "standalone agent pod must be deleted after teardown")
}

// testIncomingStealTCP proves traffic to the pod's Service is delivered to the
// LOCAL server (and the pod's own app does not answer): the probe sees the
// local response, never the pod response.
func testIncomingStealTCP(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPodWithService(t, env, "target-pod", "pod-remote")
	dumpAgentOnFailure(t, env, ns, pod)
	lsrv := startLocalServer(t, false)
	defer lsrv.stop()

	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, incomingModes(), lsrv.addr,
		[]string{env.clientBin, "hold"})

	stop := runSessionAsync(t, deps, opts, 4*time.Minute)
	defer stop()

	requireProbeEventually(t, env, ns, pod, "tcp", lsrv.response, 90*time.Second,
		"steal TCP must deliver the pod's Service traffic to the local server")
	require.Positive(t, lsrv.tcpHits(), "local server must have received the stolen TCP request")
}

// testIncomingMirrorTCP proves the LOCAL server receives a copy while the
// ORIGINAL pod app still produces the client response.
func testIncomingMirrorTCP(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPodWithService(t, env, "target-pod", "pod-remote")
	dumpAgentOnFailure(t, env, ns, pod)
	lsrv := startLocalServer(t, false)
	defer lsrv.stop()

	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageMirror, incomingModes(), lsrv.addr,
		[]string{env.clientBin, "hold"})

	stop := runSessionAsync(t, deps, opts, 4*time.Minute)
	defer stop()

	// Mirror keeps the original pod app answering the client AND delivers a copy
	// to the local server. The app answers immediately (even before the watcher
	// attaches), so this must keep DRIVING Service traffic until both hold — a
	// poll that stopped as soon as the app answered could quit before any copy
	// landed. Each iteration sends a fresh request through the Service.
	svcAddr := pod + "." + ns + ".svc.cluster.local:" + strconv.Itoa(appPort)
	var lastResp string
	require.Eventually(t, func() bool {
		lastResp = strings.TrimSpace(env.probe("tcp", svcAddr))
		return lastResp == "pod-remote" && lsrv.tcpHits() > 0
	}, 90*time.Second, 2*time.Second,
		"mirror must let the pod app answer the client AND deliver a copy to the local server (last probe: %s)", lazyString{&lastResp})
}

// testIncomingStealUDP proves UDP steal to the local server.
func testIncomingStealUDP(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPodWithService(t, env, "target-pod", "pod-remote")
	dumpAgentOnFailure(t, env, ns, pod)
	lsrv := startLocalServer(t, true)
	defer lsrv.stop()

	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, incomingModes(), lsrv.addr,
		[]string{env.clientBin, "hold"})
	opts.UDP = true

	stop := runSessionAsync(t, deps, opts, 4*time.Minute)
	defer stop()

	requireProbeEventually(t, env, ns, pod, "udp", lsrv.response, 90*time.Second,
		"steal UDP must deliver the pod's Service datagram to the local server")
	require.Positive(t, lsrv.udpHits(), "local server must have received the stolen UDP datagram")
}

// testFiles proves the injected client reads a known file from the agent's root
// via remote file redirection (Modes.Files).
func testFiles(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPod(t, env, "target-pod")
	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Files: true}, "",
		[]string{env.clientBin, "file", agentFilePath, agentFileContent})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	requireSessionOK(t, env, ns, pod, err)
}

// testEnvOverlay proves env mode overlays the target container's environment
// onto the injected command: a normal target var (HONEY_E2E_ENV_MARKER) is
// visible, while a denylisted var (PATH) stays LOCAL — the injected command's
// PATH never carries the target container's sentinel segment. The agent reads
// the target's /proc/1/environ (OpEnvGet), which the privileged ephemeral
// container (agentPrivileged, set by env.options) is free to do.
func testEnvOverlay(t *testing.T, env *e2eEnv) {
	const (
		marker      = "HONEY_E2E_ENV_MARKER"
		markerValue = "marker-value"
		// A sentinel segment planted on the target container's PATH. PATH is in the
		// data plane's built-in env denylist, so it must stay local: the injected
		// command's PATH must never contain this segment. The standard dirs are kept
		// so the target container itself still starts normally.
		targetPathSentinel = "/honey-e2e-target-only-DO-NOT-LEAK"
		targetPath         = targetPathSentinel + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	)
	ns, pod, container := newTargetPodWithEnv(t, env, "target-env", map[string]string{
		marker: markerValue,
		"PATH": targetPath,
	})
	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Egress: true, Env: true}, "",
		[]string{env.clientBin, "env", marker, markerValue, "PATH", targetPathSentinel})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	requireSessionOK(t, env, ns, pod, err)
}

// testPassthroughBaseline proves that with NO session attached, Service traffic
// reaches the pod's own app (intercept does not leak when off).
func testPassthroughBaseline(t *testing.T, env *e2eEnv) {
	ns, pod, _ := newTargetPodWithService(t, env, "target-pod", "pod-remote")
	requireProbeEventually(t, env, ns, pod, "tcp", "pod-remote", 60*time.Second,
		"without a session the pod's own app must answer")
	requireProbeEventually(t, env, ns, pod, "udp", "pod-remote", 60*time.Second,
		"without a session the pod's own app must answer UDP")
}

// testGateDeny proves a deny policy short-circuits: Session.Run returns denied,
// no ephemeral container is applied, and no start audit is emitted.
func testGateDeny(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPod(t, env, "target-pod")
	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildDenyEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Egress: true}, "",
		[]string{env.clientBin, "tcp", host(env.egressAddr), strconv.Itoa(appPort), env.egressResp})

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	require.ErrorIs(t, err, errGateDenied)

	p, getErr := env.admin.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
	require.NoError(t, getErr)
	require.Empty(t, p.Spec.EphemeralContainers, "a denied request must not deploy an agent")
	require.Empty(t, sink.snapshot(), "a denied request must not emit any audit event")
}

// testAudit proves a successful session emits intercept_start and
// intercept_stop with actor/cluster/ns/pod/mode (duration on stop), never a
// token.
func testAudit(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPod(t, env, "target-pod")
	sink := &captureSink{}
	deps, _ := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Egress: true}, "",
		[]string{env.clientBin, "tcp", host(env.egressAddr), strconv.Itoa(appPort), env.egressResp})
	opts.Actor = "e2e-actor"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	requireSessionOK(t, env, ns, pod, err)

	events := sink.snapshot()
	require.Len(t, events, 2, "a successful session emits start + stop")
	start, stop := events[0], events[1]
	require.Equal(t, actionInterceptStart, start.Action)
	require.Equal(t, actionInterceptStop, stop.Action)
	for _, ev := range events {
		require.Equal(t, "e2e-actor", ev.Actor)
		require.Equal(t, opts.Cluster, ev.Target)
		require.Equal(t, ns, ev.Extra["namespace"])
		require.Equal(t, pod, ev.Extra["pod"])
		require.Equal(t, "egress", ev.Extra["mode"])
	}
	require.NotEmpty(t, stop.Extra["duration"], "stop must record a duration")
	require.Empty(t, start.Extra["duration"], "start must not carry a duration")
	// No token ever appears in an audit event.
	for _, ev := range events {
		_, hasToken := ev.Extra["token"]
		require.False(t, hasToken, "audit events must never carry a token")
		require.Empty(t, ev.Command, "intercept audit must not record a command line")
	}
}

// testTeardown proves that after a session both port-forwards are stopped and
// the session temp dir is removed (goroutine-leak freedom is asserted by
// TestMain's goleak check).
func testTeardown(t *testing.T, env *e2eEnv) {
	ns, pod, container := newTargetPod(t, env, "target-pod")
	sink := &captureSink{}
	deps, fwd := env.deps(t, ns, pod, container, buildAllowEnforcer(t), sink)
	opts := env.options(ns, pod, container, agentImageSteal, local.Modes{Egress: true}, "",
		[]string{env.clientBin, "tcp", host(env.egressAddr), strconv.Itoa(appPort), env.egressResp})

	before := sessionTempDirs(t)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	err := New(deps, opts).Run(ctx)
	requireSessionOK(t, env, ns, pod, err)

	require.Equal(t, 2, fwd.stopCount(), "teardown must stop both port-forwards")
	require.ElementsMatch(t, []int{agentControlRemotePort, agentEgressRemotePort}, fwd.forwardedPorts(),
		"the session must forward exactly the control and egress ports")

	after := sessionTempDirs(t)
	require.Subset(t, before, after, "teardown must remove the session temp dir (no new honey-intercept-* dir remains)")
}

// ---- honey Deps + Options builders ----

func (env *e2eEnv) deps(t *testing.T, ns, pod, container string, enf *policy.Enforcer, sink *captureSink) (Deps, *recordingForwarder) {
	t.Helper()
	fwd := &recordingForwarder{cfg: env.rest, clientset: env.admin}
	deps := Deps{
		PortForwarder: fwd,
		PodExecer:     &agentExecer{cfg: env.rest, clientset: env.admin, namespace: ns, pod: pod},
		K8sClient:     env.admin,
		Enforcer:      enf,
		Sink:          sink,
		LocalRunner:   DefaultLocalRunner(),
	}
	return deps, fwd
}

func (env *e2eEnv) options(ns, pod, container, image string, modes local.Modes, target string, command []string) Options {
	return Options{
		Namespace:       ns,
		Pod:             pod,
		Container:       container,
		Cluster:         "e2e",
		AgentImage:      image,
		Target:          target,
		Modes:           modes,
		Command:         command,
		Actor:           "e2e",
		InjectorLib:     env.injectorLib,
		agentPrivileged: true,
	}
}

func incomingModes() local.Modes { return local.Modes{Egress: true, Incoming: true} }

// requireSessionOK fails with the agent + app container logs when Session.Run
// returned an error, so a data-plane failure is diagnosable.
func requireSessionOK(t *testing.T, env *e2eEnv, ns, pod string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Session.Run failed: %v\n%s", err, podDiagnostics(env, ns, pod))
	}
}

// ---- target pods / services / probe ----

// newTargetPod creates a fresh, ready single-container pod running the echo
// server (its own response is irrelevant to egress/files subtests). It returns
// namespace, pod, and the app container name.
func newTargetPod(t *testing.T, env *e2eEnv, base string) (ns, pod, container string) {
	t.Helper()
	return newTargetPodWithService(t, env, base, "pod-remote")
}

// newTargetPodWithService creates a fresh ready pod plus a Service selecting it.
func newTargetPodWithService(t *testing.T, env *e2eEnv, base, response string) (ns, pod, container string) {
	t.Helper()
	pod = uniqueName(base)
	container = "app"
	createEchoPod(t, env.admin, "default", pod, container, response)
	createService(t, env.admin, "default", pod, pod)
	waitPodReady(t, env.admin, "default", pod, 3*time.Minute)
	return "default", pod, container
}

// newTargetPodWithEnv creates a fresh, ready single-container echo pod carrying
// the given container environment variables (no Service), used by the
// env_overlay subtest to plant a known target var — and a denylisted PATH
// sentinel — the injected command reads back. It returns namespace, pod, and the
// app container name.
func newTargetPodWithEnv(t *testing.T, env *e2eEnv, base string, envVars map[string]string) (ns, pod, container string) {
	t.Helper()
	pod = uniqueName(base)
	container = "app"
	createEchoPodWithEnv(t, env.admin, "default", pod, container, "pod-remote", envVars)
	waitPodReady(t, env.admin, "default", pod, 3*time.Minute)
	return "default", pod, container
}

func createEchoPod(t *testing.T, admin *kubernetes.Clientset, ns, name, container, response string) {
	t.Helper()
	createEchoPodWithEnv(t, admin, ns, name, container, response, nil)
}

// createEchoPodWithEnv is createEchoPod with an optional container environment.
// A nil/empty envVars map leaves the pod spec byte-identical to the plain echo
// pod every other subtest uses.
func createEchoPodWithEnv(t *testing.T, admin *kubernetes.Clientset, ns, name, container, response string, envVars map[string]string) {
	t.Helper()
	var envList []corev1.EnvVar
	for key, value := range envVars {
		envList = append(envList, corev1.EnvVar{Name: key, Value: value})
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": name}},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:            container,
				Image:           targetImage,
				ImagePullPolicy: corev1.PullNever,
				Args:            []string{"server", "--address=:" + strconv.Itoa(appPort), "--response=" + response},
				Env:             envList,
				Ports: []corev1.ContainerPort{
					{ContainerPort: appPort, Protocol: corev1.ProtocolTCP},
					{ContainerPort: appPort, Protocol: corev1.ProtocolUDP},
				},
			}},
		},
	}
	_, err := admin.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.CoreV1().Pods(ns).Delete(context.Background(), name, *metav1.NewDeleteOptions(0))
	})
}

func createEchoDeployment(t *testing.T, admin *kubernetes.Clientset, ns, name, response string) {
	t.Helper()
	// A bare pod is enough (single replica), but label it so a Service selects it.
	createEchoPod(t, admin, ns, name, "app", response)
	waitPodReady(t, admin, ns, name, 3*time.Minute)
}

// createService creates a ClusterIP Service (TCP + UDP on appPort) selecting the
// pod labelled app=selector, and returns the allocated ClusterIP.
func createService(t *testing.T, admin *kubernetes.Clientset, ns, name, selector string) string {
	t.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": selector},
			Ports: []corev1.ServicePort{
				{Name: "tcp", Port: appPort, TargetPort: intstr.FromInt32(appPort), Protocol: corev1.ProtocolTCP},
				{Name: "udp", Port: appPort, TargetPort: intstr.FromInt32(appPort), Protocol: corev1.ProtocolUDP},
			},
		},
	}
	created, err := admin.CoreV1().Services(ns).Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.CoreV1().Services(ns).Delete(context.Background(), name, *metav1.NewDeleteOptions(0))
	})
	return created.Spec.ClusterIP
}

// createProbePod runs a long-lived pod (target image, sleeping) whose shell is
// used to send one-shot requests into a Service from inside the cluster.
func createProbePod(t *testing.T, admin *kubernetes.Clientset, ns, name string) {
	t.Helper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:            "probe",
				Image:           targetImage,
				ImagePullPolicy: corev1.PullNever,
				Command:         []string{"sleep", "36000"},
			}},
		},
	}
	_, err := admin.CoreV1().Pods(ns).Create(context.Background(), pod, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.CoreV1().Pods(ns).Delete(context.Background(), name, *metav1.NewDeleteOptions(0))
	})
}

// requireProbeEventually repeatedly probes the target Service from the in-cluster
// probe pod until the response equals want (or the timeout elapses).
func requireProbeEventually(t *testing.T, env *e2eEnv, ns, pod, network, want string, timeout time.Duration, msg string) {
	t.Helper()
	addr := pod + "." + ns + ".svc.cluster.local:" + strconv.Itoa(appPort)
	var last string
	require.Eventually(t, func() bool {
		last = env.probe(network, addr)
		return strings.TrimSpace(last) == want
	}, timeout, 2*time.Second, "%s (want %q; last probe: %s)", msg, want, lazyString{&last})
}

// lazyString formats the pointed-to string when the message is rendered (at
// assertion-failure time), so a probe's final value appears in the failure.
type lazyString struct{ s *string }

func (l lazyString) String() string { return strings.TrimSpace(*l.s) }

// probe execs a one-shot request into the probe pod and returns the response.
func (env *e2eEnv) probe(network, address string) string {
	client := &k8sprovider.K8sNativeClient{
		Config:    env.rest,
		Clientset: env.admin,
		Namespace: "default",
		PodName:   env.probePod,
		Container: "probe",
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	_ = client.ExecInPod(ctx, []string{
		"/usr/local/bin/mogate-fixture", "request",
		"--network", network, "--address", address, "--timeout", "8s",
	}, nil, &out, io.Discard, false, nil)
	return out.String()
}

// ---- local server (incoming steal/mirror target) ----

type localServer struct {
	addr     string
	response string
	tcp      int
	udp      int
	mu       sync.Mutex
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func (s *localServer) tcpHits() int { s.mu.Lock(); defer s.mu.Unlock(); return s.tcp }
func (s *localServer) udpHits() int { s.mu.Lock(); defer s.mu.Unlock(); return s.udp }
func (s *localServer) stop()        { s.cancel(); s.wg.Wait() }

// startLocalServer binds a TCP (and, when udp, a UDP) echo server on an
// ephemeral 127.0.0.1 port, recording every request it receives.
func startLocalServer(t *testing.T, udp bool) *localServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	s := &localServer{response: "local-server-resp", cancel: cancel}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.addr = ln.Addr().String()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		go func() { <-ctx.Done(); _ = ln.Close() }()
		for {
			conn, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			s.mu.Lock()
			s.tcp++
			s.mu.Unlock()
			go func(c net.Conn) {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				buf := make([]byte, 1024)
				_, _ = c.Read(buf)
				_, _ = c.Write([]byte(s.response))
			}(conn)
		}
	}()

	if udp {
		_, portStr, _ := net.SplitHostPort(s.addr)
		port, _ := strconv.Atoi(portStr)
		uconn, uerr := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
		require.NoError(t, uerr)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			go func() { <-ctx.Done(); _ = uconn.Close() }()
			buf := make([]byte, 65535)
			for {
				n, client, rerr := uconn.ReadFromUDP(buf)
				if rerr != nil {
					return
				}
				s.mu.Lock()
				s.udp++
				s.mu.Unlock()
				_, _ = uconn.WriteToUDP([]byte(s.response), client)
				_ = n
			}
		}()
	}
	return s
}

// ---- async session driver ----

// runSessionAsync runs Session.Run in the background and returns a stop function
// that cancels it and waits for a clean (context.Canceled) return.
func runSessionAsync(t *testing.T, deps Deps, opts Options, timeout time.Duration) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	res := make(chan error, 1)
	go func() { res <- New(deps, opts).Run(ctx) }()

	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-res:
				if err != nil && ctx.Err() == nil {
					t.Errorf("session returned an unexpected error: %v", err)
				}
			case <-time.After(shutdownGrace + 10*time.Second):
				t.Error("session did not return after cancel")
			}
		})
	}
}

// ---- real PortForwarder (SPDY) with stop/forward recording ----

type recordingForwarder struct {
	cfg       *rest.Config
	clientset kubernetes.Interface
	mu        sync.Mutex
	forwarded []int
	stops     int
}

func (f *recordingForwarder) forwardedPorts() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.forwarded...)
}

func (f *recordingForwarder) stopCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stops
}

func (f *recordingForwarder) Forward(ctx context.Context, _, namespace, pod string, remotePort int) (string, func(), error) {
	reqURL, err := url.Parse(fmt.Sprintf("%s/api/v1/namespaces/%s/pods/%s/portforward", f.cfg.Host, namespace, pod))
	if err != nil {
		return "", nil, fmt.Errorf("build port-forward url: %w", err)
	}
	transport, upgrader, err := spdy.RoundTripperFor(f.cfg)
	if err != nil {
		return "", nil, fmt.Errorf("spdy round tripper: %w", err)
	}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, reqURL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	var once sync.Once
	stop := func() {
		once.Do(func() {
			close(stopCh)
			f.mu.Lock()
			f.stops++
			f.mu.Unlock()
		})
	}

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, io.Discard, io.Discard)
	if err != nil {
		stop()
		return "", nil, fmt.Errorf("create port forwarder: %w", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()

	select {
	case <-readyCh:
	case err := <-errCh:
		stop()
		return "", nil, fmt.Errorf("port-forward %s/%s: %w", namespace, pod, err)
	case <-ctx.Done():
		stop()
		return "", nil, fmt.Errorf("port-forward %s/%s: %w", namespace, pod, ctx.Err())
	}

	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		stop()
		return "", nil, fmt.Errorf("resolve local port: %w", err)
	}
	localAddr := fmt.Sprintf("127.0.0.1:%d", ports[0].Local)
	// honey's Session.Run establishes the forwards and then immediately runs the
	// local injection session; the in-Pod agent, however, binds its control and
	// egress ports only after it installs its redirects, a window that widens
	// under load. Block here until the agent has logged that it is serving so the
	// local session (whose incoming watcher dials the control port exactly once)
	// never races a not-yet-listening agent. This lives in the test's
	// PortForwarder, not honey, so honey's production behaviour is untouched.
	f.waitAgentServing(ctx, namespace, pod, 90*time.Second)
	f.mu.Lock()
	f.forwarded = append(f.forwarded, remotePort)
	f.mu.Unlock()
	return localAddr, stop, nil
}

// agentServingLog is the substring the agent prints to stderr immediately before
// it starts serving its control and egress ports.
const agentServingLog = "kube-agent:"

// waitAgentServing blocks until the pod's agent (most-recently added ephemeral
// container) logs that it is serving, plus a brief grace for the listeners to
// bind. Polling the log is a reliable readiness signal (a bare port dial cannot
// distinguish a listening agent that rejects an unauthenticated probe from one
// that is not up yet).
func (f *recordingForwarder) waitAgentServing(ctx context.Context, ns, pod string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		if strings.Contains(agentContainerLogs(ctx, f.clientset, ns, pod), agentServingLog) {
			time.Sleep(time.Second) // let the listeners finish binding
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// agentContainerLogs returns the recent logs of the pod's most-recently added
// ephemeral container (this session's agent), or "" if unavailable.
func agentContainerLogs(ctx context.Context, clientset kubernetes.Interface, ns, pod string) string {
	p, err := clientset.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil || len(p.Spec.EphemeralContainers) == 0 {
		return ""
	}
	name := p.Spec.EphemeralContainers[len(p.Spec.EphemeralContainers)-1].Name
	req := clientset.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{Container: name, TailLines: ptrInt64(50)})
	rc, err := req.Stream(ctx)
	if err != nil {
		return ""
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	return string(data)
}

// ---- real PodExecer (token delivery into the agent container) ----

type agentExecer struct {
	cfg       *rest.Config
	clientset kubernetes.Interface
	namespace string
	pod       string
	// container names the agent container directly, skipping the
	// ephemeral-container lookup below. Set by the targetless subtest, whose
	// standalone pod's single container has a known, fixed name
	// (AgentContainerName). Left empty for the targeted subtests, which fall
	// back to resolving the most recently added ephemeral container — mirrors
	// interceptPodExecer's container field (internal/cli/intercept.go).
	container string
}

func (e *agentExecer) ExecInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	container := e.container
	if container == "" {
		p, err := e.clientset.CoreV1().Pods(e.namespace).Get(ctx, e.pod, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get pod %q: %w", e.pod, err)
		}
		ecs := p.Spec.EphemeralContainers
		if len(ecs) == 0 {
			return fmt.Errorf("no agent container on pod %q", e.pod)
		}
		container = ecs[len(ecs)-1].Name
	}
	client := &k8sprovider.K8sNativeClient{
		Config:    e.cfg,
		Clientset: e.clientset,
		Namespace: e.namespace,
		PodName:   e.pod,
		Container: container,
	}
	return client.ExecInPod(ctx, cmd, stdin, stdout, stderr, false, nil)
}

// ---- capturing audit sink (concurrency-safe) ----

type captureSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (s *captureSink) Log(_ context.Context, e audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

func (s *captureSink) Close() error { return nil }

func (s *captureSink) snapshot() []audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.Event(nil), s.events...)
}

// ---- policy enforcers ----

func buildAllowEnforcer(t *testing.T) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "intercept.rego", `package honey
default allow := false
allow if input.action == "intercept"`)
	require.NoError(t, err)
	return enf
}

func buildDenyEnforcer(t *testing.T) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "intercept.rego", `package honey
default allow := false`)
	require.NoError(t, err)
	return enf
}

// ---- k3s + image lifecycle ----

func requireDocker(t *testing.T) {
	t.Helper()
	if os.Getenv("DOCKER_HOST") == "" {
		out, err := exec.Command("docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}").Output()
		if err != nil {
			t.Skipf("no Docker host available: %v", err)
		}
		t.Setenv("DOCKER_HOST", strings.TrimSpace(string(out)))
	}
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skipf("Docker daemon unavailable: %v", err)
	}
}

func startK3s(t *testing.T) (*rest.Config, *kubernetes.Clientset, *k3s.K3sContainer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	container, err := k3s.Run(ctx, e2eK3sImage)
	if err != nil {
		cancel()
		t.Skipf("k3s/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() {
		if terr := testcontainers.TerminateContainer(container); terr != nil {
			t.Logf("terminate k3s container: %v", terr)
		}
	})
	t.Cleanup(cancel)

	kubeBytes, err := container.GetKubeConfig(ctx)
	require.NoError(t, err)
	rc, err := clientcmd.RESTConfigFromKubeConfig(kubeBytes)
	require.NoError(t, err)
	admin, err := kubernetes.NewForConfig(rc)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		_, verr := admin.Discovery().ServerVersion()
		return verr == nil
	}, 90*time.Second, time.Second, "k3s API server did not become ready")
	return rc, admin, container
}

// cloneMogate performs a shallow clone of the pinned data-plane module.
func cloneMogate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "clone", "--depth", "1", "--branch", mogateTag, mogateRepo, dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("clone mogate %s: %v\n%s", mogateTag, err, out)
	}
	return dir
}

// buildHostInjector `make build`s the host injector library, returning its path
// (libmogate.dylib on darwin, .so on linux). The module's build target runs its
// own code generation first, so a fresh checkout builds directly.
func buildHostInjector(t *testing.T, mogateDir string) string {
	t.Helper()
	runOrSkip(t, mogateDir, "make", "build")
	for _, name := range []string{"libmogate.dylib", "libmogate.so"} {
		p := filepath.Join(mogateDir, "bin", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("mogate make build did not produce an injector library")
	return ""
}

// buildImages builds the target echo image and the steal/mirror agent images.
// The agent images layer a thin entrypoint on top of the GENUINE mogate agent
// image (built from the module's own unmodified Dockerfile) only to inject
// test-timing flags; mirror additionally sets --mode=mirror and a larger
// --mirror-claim-window. All are single-platform so they save and import into
// the k3s node cleanly.
func buildImages(t *testing.T, mogateDir string) {
	t.Helper()
	// The genuine agent image, straight from the module's own Dockerfile.
	dockerBuild(t, mogateDir, "Dockerfile", agentBaseImage)

	// A thin entrypoint layered on the genuine image. honey delivers the token
	// out of band (via exec) AFTER this container starts; kube-agent waits for
	// its --token-file natively, so this wrapper does NOT wait — it only injects
	// test-timing flags (the capture mode and its claim window come from
	// HONEY_AGENT_EXTRA; the larger --claim-timeout absorbs the latency of
	// claiming a stolen stream over a doubly-nested (colima) port-forward;
	// production clusters use the defaults and need no wrapper at all).
	entrypoint := `#!/bin/sh
sub="$1"; shift
exec /usr/local/bin/mogate "$sub" --claim-timeout=15s ${HONEY_AGENT_EXTRA:-} "$@"
`
	writeFile(t, filepath.Join(mogateDir, "honey-agent-entrypoint.sh"), entrypoint)

	wrapperDockerfile := "FROM " + agentBaseImage + `
USER root
COPY honey-agent-entrypoint.sh /usr/local/bin/honey-agent-entrypoint.sh
RUN chmod +x /usr/local/bin/honey-agent-entrypoint.sh && printf '%s' "` + agentFileContent + `" > ` + agentFilePath + `
ENTRYPOINT ["/usr/local/bin/honey-agent-entrypoint.sh"]
`
	writeFile(t, filepath.Join(mogateDir, "Dockerfile.honeyagent"), wrapperDockerfile)

	// Mirror raises the mirror-mode claim window (default 200ms) so the local
	// watcher can still claim a copy over colima's high-latency port-forward.
	mirrorDockerfile := "FROM " + agentImageSteal + "\nENV HONEY_AGENT_EXTRA=\"--mode=mirror --mirror-claim-window=5s\"\n"
	writeFile(t, filepath.Join(mogateDir, "Dockerfile.honeyagentmirror"), mirrorDockerfile)

	dockerBuild(t, mogateDir, "test/e2e/Dockerfile.fixture", targetImage)
	dockerBuild(t, mogateDir, "Dockerfile.honeyagent", agentImageSteal)
	dockerBuild(t, mogateDir, "Dockerfile.honeyagentmirror", agentImageMirror)
}

func dockerBuild(t *testing.T, contextDir, dockerfile, tag string) {
	t.Helper()
	cmd := exec.Command("docker", "build", "--provenance=false", "--platform=linux/amd64",
		"-f", filepath.Join(contextDir, dockerfile), "-t", tag, contextDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("docker build %s: %v\n%s", tag, err, tailBytes(out, 40))
	}
}

// importImages saves the locally-built images and imports them into the k3s
// node's containerd (so imagePullPolicy: Never can use them).
func importImages(t *testing.T, container *k3s.K3sContainer) {
	t.Helper()
	ctx := context.Background()
	tar := filepath.Join(t.TempDir(), "images.tar")
	// agentBaseImage is included alongside the wrapper images because the
	// targetless subtest deploys it directly (no wrapper) as the standalone
	// agent Pod's image.
	save := exec.Command("docker", "save", "-o", tar, targetImage, agentBaseImage, agentImageSteal, agentImageMirror)
	if out, err := save.CombinedOutput(); err != nil {
		t.Fatalf("docker save images: %v\n%s", err, out)
	}
	require.NoError(t, container.CopyFileToContainer(ctx, tar, "/tmp/honey-images.tar", 0o600))

	require.Eventually(t, func() bool {
		code, _, err := container.Exec(ctx, []string{"/bin/sh", "-c", "test -S /run/k3s/containerd/containerd.sock"})
		return err == nil && code == 0
	}, 60*time.Second, 500*time.Millisecond, "k3s containerd socket did not appear")

	code, reader, err := container.Exec(ctx, []string{
		"/bin/ctr", "--address", "/run/k3s/containerd/containerd.sock",
		"--namespace", "k8s.io", "images", "import", "/tmp/honey-images.tar",
	})
	body, _ := io.ReadAll(reader)
	if err != nil || code != 0 {
		t.Fatalf("import images into k3s: code=%d err=%v\n%s", code, err, body)
	}
}

// buildInjectedclient compiles the injected client (a libc program so the
// injector's DYLD/LD_PRELOAD hooks apply) into a fresh temp path.
func buildInjectedClient(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "client.c")
	writeFile(t, src, injectedClientC)
	bin := filepath.Join(dir, "injected-client")
	cmd := exec.Command("cc", "-O2", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("compile injected client: %v\n%s", err, out)
	}
	return bin
}

// ---- misc helpers ----

// waitForClusterDNS waits until CoreDNS reports at least one ready replica, so
// in-pod name resolution (egress_dns) and the in-cluster probe can resolve
// Service names.
func waitForClusterDNS(t *testing.T, admin *kubernetes.Clientset, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		d, err := admin.AppsV1().Deployments("kube-system").Get(context.Background(), "coredns", metav1.GetOptions{})
		return err == nil && d.Status.ReadyReplicas >= 1
	}, timeout, 2*time.Second, "cluster DNS (coredns) did not become ready")
}

func waitPodReady(t *testing.T, admin *kubernetes.Clientset, ns, name string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		p, err := admin.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil || p.Status.Phase != corev1.PodRunning || len(p.Status.ContainerStatuses) == 0 {
			return false
		}
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				return false
			}
		}
		return true
	}, timeout, time.Second, "pod %s/%s did not become ready", ns, name)
}

// dumpAgentOnFailure logs the agent + app container output when the subtest
// fails, so an incoming delivery failure (which asserts via a probe, not
// Session.Run) is diagnosable.
func dumpAgentOnFailure(t *testing.T, env *e2eEnv, ns, pod string) {
	t.Helper()
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("incoming diagnostics for %s/%s:\n%s", ns, pod, podDiagnostics(env, ns, pod))
		}
	})
}

func podDiagnostics(env *e2eEnv, ns, pod string) string {
	var b strings.Builder
	p, err := env.admin.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
	if err != nil {
		return "diagnostics: " + err.Error()
	}
	fmt.Fprintf(&b, "pod phase=%s\n", p.Status.Phase)
	for _, cs := range p.Status.EphemeralContainerStatuses {
		if cs.State.Waiting != nil {
			fmt.Fprintf(&b, "--- %s waiting: %s: %s\n", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
		}
	}
	for _, ec := range p.Spec.EphemeralContainers {
		b.WriteString(containerLog(env, ns, pod, ec.Name))
	}
	if len(p.Spec.Containers) > 0 {
		b.WriteString(containerLog(env, ns, pod, p.Spec.Containers[0].Name))
	}
	return b.String()
}

func containerLog(env *e2eEnv, ns, pod, container string) string {
	req := env.admin.CoreV1().Pods(ns).GetLogs(pod, &corev1.PodLogOptions{Container: container, TailLines: ptrInt64(80)})
	rc, err := req.Stream(context.Background())
	if err != nil {
		return fmt.Sprintf("--- %s logs: %v\n", container, err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	return fmt.Sprintf("--- %s logs ---\n%s\n", container, data)
}

// pollPodDiagnostics polls podDiagnostics for ns/pod every 2s in the
// background and keeps the most recent snapshot. It exists for the
// targetless subtest: Session.Run deletes the standalone agent pod as part of
// its own teardown BEFORE Run returns (unlike the targeted path's ephemeral
// container, which nothing ever removes), so a diagnostics fetch AFTER Run
// returns would only ever see "not found". Polling while the session runs
// captures the pod's last live status and logs instead.
//
// The returned stop function cancels the poller, waits for it to exit (so no
// goroutine survives the calling test — TestMain asserts via goleak that none
// do), and returns the frozen snapshot.
func pollPodDiagnostics(env *e2eEnv, ns, pod string) (stop func() string) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	snapshot := "no diagnostics captured"
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			s := podDiagnostics(env, ns, pod)
			mu.Lock()
			snapshot = s
			mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() string {
		cancel()
		<-done
		mu.Lock()
		defer mu.Unlock()
		return snapshot
	}
}

// sessionTempDirs lists the honey-intercept-* session directories currently in
// the system temp dir (used to prove teardown removed the session's dir).
func sessionTempDirs(t *testing.T) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(os.TempDir(), sessionDirPattern+"*"))
	require.NoError(t, err)
	return matches
}

func runOrSkip(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func uniqueName(prefix string) string { return prefix + "-" + strings.ToLower(k8srand.String(6)) }

func host(hostPort string) string {
	h, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return hostPort
	}
	return h
}

func tailBytes(b []byte, lines int) string {
	parts := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

func ptrInt64(v int64) *int64 { return &v }

// injectedClientC is the source of the libc client injected under the mogate
// shim. It retries each action until a deadline (absorbing agent startup) and
// bounds each attempt with SIGALRM.
const injectedClientC = `#include <errno.h>
#include <fcntl.h>
#include <netdb.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

static void on_alarm(int sig) { (void)sig; }

static int do_net(const char *proto, const char *host, const char *port, const char *expected) {
    struct addrinfo hints;
    memset(&hints, 0, sizeof(hints));
    hints.ai_family = AF_UNSPEC;
    hints.ai_socktype = strcmp(proto, "udp") == 0 ? SOCK_DGRAM : SOCK_STREAM;
    struct addrinfo *res = NULL;
    if (getaddrinfo(host, port, &hints, &res) != 0) return -1;
    int rc = -1;
    for (struct addrinfo *a = res; a; a = a->ai_next) {
        int fd = socket(a->ai_family, a->ai_socktype, a->ai_protocol);
        if (fd < 0) continue;
        if (connect(fd, a->ai_addr, a->ai_addrlen) != 0) { close(fd); continue; }
        const char *msg = "honey-probe";
        if (write(fd, msg, strlen(msg)) < 0) { close(fd); continue; }
        char buf[512];
        ssize_t n = read(fd, buf, sizeof(buf) - 1);
        close(fd);
        if (n <= 0) { rc = -1; continue; }
        buf[n] = 0;
        rc = strcmp(buf, expected) == 0 ? 0 : -2;
        break;
    }
    freeaddrinfo(res);
    return rc;
}

static int do_file(const char *path, const char *expected) {
    int fd = open(path, O_RDONLY);
    if (fd < 0) return -1;
    char buf[4096];
    ssize_t n = read(fd, buf, sizeof(buf) - 1);
    close(fd);
    if (n < 0) return -1;
    buf[n] = 0;
    return strcmp(buf, expected) == 0 ? 0 : -2;
}

int main(int argc, char **argv) {
    if (argc >= 2 && strcmp(argv[1], "hold") == 0) { sleep(300); return 0; }
    // env <name> <expected> <denyname> <deny-sentinel-substr>: proves the env-mode
    // overlay. getenv reads the child's process environment, which local.Run has
    // already overlaid with the target container's before exec — no relay/injector
    // hook is needed and no retry loop applies (the overlay is set synchronously
    // before the command spawns). Exits 0 only when the target var was overlaid AND
    // the denylisted var did NOT leak the target's value.
    if (argc >= 2 && strcmp(argv[1], "env") == 0) {
        if (argc < 6) { fprintf(stderr, "usage: client env name expected denyname deny-sentinel\n"); return 2; }
        const char *got = getenv(argv[2]);
        if (got == NULL || strcmp(got, argv[3]) != 0) {
            fprintf(stderr, "env overlay: %s=%s want %s\n", argv[2], got ? got : "(unset)", argv[3]);
            return 1;
        }
        const char *deny = getenv(argv[4]);
        if (deny != NULL && strstr(deny, argv[5]) != NULL) {
            fprintf(stderr, "denylisted var %s leaked target value (contains %s): %s\n", argv[4], argv[5], deny);
            return 1;
        }
        return 0;
    }
    if (argc < 4) { fprintf(stderr, "usage: client tcp|udp host port expected | file path expected | env name expected denyname deny-sentinel | hold\n"); return 2; }

    struct sigaction sa;
    memset(&sa, 0, sizeof(sa));
    sa.sa_handler = on_alarm;
    sigaction(SIGALRM, &sa, NULL);

    const char *mode = argv[1];
    time_t deadline = time(NULL) + 25;
    int last = -1;
    while (time(NULL) < deadline) {
        alarm(6);
        if (strcmp(mode, "file") == 0) {
            last = do_file(argv[2], argv[3]);
        } else if (argc >= 5) {
            last = do_net(mode, argv[2], argv[3], argv[4]);
        } else {
            last = 2;
        }
        alarm(0);
        if (last == 0) return 0;
        usleep(300 * 1000);
    }
    fprintf(stderr, "injected client failed: mode=%s last=%d\n", mode, last);
    return 1;
}
`
