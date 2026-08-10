//go:build k8s_e2e

// Package k8sproxy k8s_e2e matrix: exercises the security- and option-paths of
// the Kubernetes access proxy against a REAL k3s cluster via testcontainers,
// driven through the REAL production entrypoint (RunServer + its mTLS listener).
// Where the sibling TestK8sProxyE2E_Impersonation proves the happy
// impersonation path, this file proves the paths that MUST hold for the
// boundary to be safe.
//
// Every proxy behaviour is proven with a REAL kubectl binary run inside an
// alpine/k8s container that reaches the host-side proxy over testcontainers'
// HostAccessPorts tunnel — the proof is kubectl's exit code + stdout/stderr, not
// an in-process client. Admin-side fixtures (RBAC, namespaces, pods) and reading
// honey's own in-process audit sink use the admin clientset: that is arranging
// the world and inspecting honey's audit, not exercising the proxy.
//
// Excluded from the normal `go test` run (and CI) by the k8s_e2e build tag.
// Requires a reachable Docker daemon; the ONLY skips in this file are the two
// environment gates (Docker-unavailable in startK3s, kubectl-container-
// unavailable in the runner). Every proxy assertion is mandatory (require). One
// k3s cluster is started for the whole matrix; each subtest sets up its OWN
// uniquely-named RBAC + its OWN proxy instance (fresh enforcer/sink) so subtests
// cannot interfere. Run explicitly:
//
//	go test -tags k8s_e2e -run TestK8sProxyE2E_Matrix -v ./internal/k8sproxy/ -timeout 40m
package k8sproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/wait"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8srand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/policy"
)

// matrixK3sImage pins the same k3s image the sibling impersonation test uses.
const matrixK3sImage = "rancher/k3s:v1.31.5-k3s1"

// matrixKubectlImage bundles kubectl + a shell so kubectl can be driven inside a
// container reaching the host-side proxy over testcontainers' HostAccessPorts
// tunnel, without a host-installed kubectl binary.
const matrixKubectlImage = "alpine/k8s:1.31.7"

// portForwardScript establishes a kubectl port-forward through the proxy to a
// pod's HTTP port and curls it from inside the container. __POD__/__NS__ are
// substituted per subtest. It prints the served body on success.
const portForwardScript = `kubectl port-forward pod/__POD__ -n __NS__ 18080:8080 >/tmp/pf.log 2>&1 &
PF=$!
RESULT=""
i=0
while [ $i -lt 15 ]; do
  BODY=$(wget -qO- http://127.0.0.1:18080/ 2>/dev/null)
  if [ -n "$BODY" ]; then RESULT="$BODY"; break; fi
  i=$((i+1)); sleep 1
done
kill $PF 2>/dev/null
if [ -n "$RESULT" ]; then echo "$RESULT"; else echo PF-FAIL; cat /tmp/pf.log; fi
`

// TestK8sProxyE2E_Matrix runs the security/option matrix serially against ONE
// shared k3s cluster. Each subtest is hermetic (unique RBAC, namespaces, and its
// own proxy) and proves proxy behaviour exclusively through kubectl.
func TestK8sProxyE2E_Matrix(t *testing.T) {
	adminRest, admin := startK3s(t)

	// One throwaway CA signs the client cert "alice" presents; it is every
	// subtest proxy's client-CA trust anchor. The mapped Impersonate-User is
	// alice's CN; RBAC below always binds a GROUP (never the user), so the
	// shared user cert never causes cross-subtest interference.
	ca := newTestCA(t)
	aliceCert := ca.clientCert(t)
	aliceCertPEM, aliceKeyPEM := pemFromCert(t, aliceCert)

	// (1) OPA denies BEFORE the cluster is consulted: RBAC would allow, the gate
	// blocks anyway. kubectl: `kubectl get pods -n default` -> Forbidden.
	t.Run("opa_deny_blocks_before_cluster", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		group := uniqueName("grp")
		cluster := uniqueName("cl")
		grantClusterRole(t, ctx, admin, group, []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		})
		enf := enforcerFromRego(t, ctx, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if {
	input.action == "k8s_request"
	input.verb == "list"
	input.resource == "pods"
}
deny_reason := "listing pods is blocked by policy" if {
	input.action == "k8s_request"
	input.verb == "list"
	input.resource == "pods"
}
`)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{group}, ca.certPEM)
		kc := newKubectlRunner(t, ctx, addr, cluster, aliceCertPEM, aliceKeyPEM)

		out, code := kc.run("get", "pods", "-n", "default")
		require.NotEqual(t, 0, code, "OPA must block the list even though RBAC allows it: %s", truncateOutput(out))
		require.Contains(t, strings.ToLower(out), "forbidden", "expected a forbidden error from the proxy: %s", truncateOutput(out))
		require.True(t, auditMatch(sink.snapshot(), "deny", "alice", cluster, "list", "pods"),
			"expected a deny k8s_request audit event for list/pods, got: %+v", sink.snapshot())
	})

	// (2) Scoped policy: allow get/list pods, deny delete, against a cluster
	// whose RBAC would permit the delete. kubectl get succeeds; kubectl delete
	// is Forbidden by the proxy.
	t.Run("opa_scoped_allows_get_denies_delete", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		group := uniqueName("grp")
		cluster := uniqueName("cl")
		ns := uniqueName("ns")
		createNamespace(t, ctx, admin, ns)
		victim := "victim"
		// A real pod so a `kubectl delete` reaches the DELETE verb (not a 404).
		createPod(t, ctx, admin, ns, victim, []string{"sleep", "3600"})

		grantClusterRole(t, ctx, admin, group, []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "delete"}},
		})
		enf := enforcerFromRego(t, ctx, `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if { input.verb == "delete" }
deny_reason := "deletes are not permitted by policy" if { input.verb == "delete" }
`)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{group}, ca.certPEM)
		kc := newKubectlRunner(t, ctx, addr, cluster, aliceCertPEM, aliceKeyPEM)

		// get/list allowed (Eventually: RBAC authorization-cache lag).
		require.Eventually(t, func() bool {
			_, code := kc.run("get", "pods", "-n", ns)
			return code == 0
		}, 30*time.Second, 2*time.Second, "kubectl get pods must succeed under the scoped policy")

		out, code := kc.run("delete", "pod", victim, "-n", ns)
		require.NotEqual(t, 0, code, "delete must be denied by policy: %s", truncateOutput(out))
		require.Contains(t, strings.ToLower(out), "forbidden", "delete must be forbidden: %s", truncateOutput(out))
		require.True(t, auditMatch(sink.snapshot(), "deny", "alice", cluster, "delete", "pods"),
			"expected a deny k8s_request audit event for delete/pods, got: %+v", sink.snapshot())
	})

	// (3) Impersonation smuggling: `kubectl --as/--as-group` sends real
	// Impersonate-* headers for cluster-admin/system:masters. The proxy must
	// strip them; `kubectl auth whoami` through the proxy reports alice + the
	// mapped group, even though the real API server would honor system:masters.
	t.Run("impersonation_smuggling_e2e", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		group := uniqueName("grp")
		cluster := uniqueName("cl")
		// No RBAC grant needed: SelfSubjectReview is allowed for any
		// authenticated identity (system:basic-user).
		enf, err := policy.New(ctx, "", nil)
		require.NoError(t, err)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{group}, ca.certPEM)
		kc := newKubectlRunner(t, ctx, addr, cluster, aliceCertPEM, aliceKeyPEM)

		out, code := kc.run("auth", "whoami", "--as=cluster-admin", "--as-group=system:masters", "-o", "json")
		require.Equal(t, 0, code, "kubectl auth whoami failed: %s", truncateOutput(out))

		review := parseSelfSubjectReview(t, out)
		require.Equal(t, "alice", review.Status.UserInfo.Username,
			"proxy must impersonate alice, not the client-supplied cluster-admin: %s", truncateOutput(out))
		require.Contains(t, review.Status.UserInfo.Groups, group, "mapped group must be present")
		require.NotContains(t, review.Status.UserInfo.Groups, "system:masters",
			"client-smuggled system:masters must have been stripped")
	})

	// (4) Unknown cluster: kubectl targets an unregistered path prefix; the
	// proxy returns a generic 404 (never revealing which clusters exist).
	t.Run("unknown_cluster_404", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		cluster := uniqueName("cl")
		enf, err := policy.New(ctx, "", nil)
		require.NoError(t, err)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{uniqueName("grp")}, ca.certPEM)
		// Runner targets a DIFFERENT (unregistered) cluster path prefix.
		kc := newKubectlRunner(t, ctx, addr, uniqueName("nope"), aliceCertPEM, aliceKeyPEM)

		out, code := kc.run("get", "pods", "-n", "default")
		require.NotEqual(t, 0, code, "an unknown cluster must fail: %s", truncateOutput(out))
		low := strings.ToLower(out)
		require.True(t,
			strings.Contains(low, "could not find") || strings.Contains(low, "404") || strings.Contains(low, "not found"),
			"expected a 404/not-found error for an unknown cluster: %s", truncateOutput(out))
	})

	// (5) Untrusted / absent client certs are rejected by mTLS at the handshake
	// (RequireAndVerifyClientCert), never reaching the handler or the sink.
	t.Run("untrusted_client_cert_rejected", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		cluster := uniqueName("cl")
		enf, err := policy.New(ctx, "", nil)
		require.NoError(t, err)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{uniqueName("grp")}, ca.certPEM)

		// A cert signed by a DIFFERENT CA (not the proxy's client-CA anchor).
		rogueCA := newTestCA(t)
		rogueCert := rogueCA.clientCert(t)
		rogueCertPEM, rogueKeyPEM := pemFromCert(t, rogueCert)
		kc := newKubectlRunner(t, ctx, addr, cluster, rogueCertPEM, rogueKeyPEM)

		out, code := kc.run("get", "pods", "-n", "default")
		require.NotEqual(t, 0, code, "an untrusted client cert must be rejected: %s", truncateOutput(out))
		require.True(t, tlsRejected(out), "expected a TLS handshake rejection: %s", truncateOutput(out))
		require.Empty(t, sink.snapshot(), "a rejected handshake must never reach the handler/audit")

		t.Run("no_client_cert_rejected", func(t *testing.T) {
			// nil cert/key => kubeconfig with NO client certificate.
			kcNoCert := newKubectlRunner(t, ctx, addr, cluster, nil, nil)
			out, code := kcNoCert.run("get", "pods", "-n", "default")
			require.NotEqual(t, 0, code, "a client with no cert must be rejected: %s", truncateOutput(out))
			require.True(t, tlsRejected(out), "expected a TLS handshake rejection: %s", truncateOutput(out))
			require.Empty(t, sink.snapshot(), "a rejected handshake must never reach the handler/audit")
		})
	})

	// (6) Streaming exec: `kubectl exec <pod> -- echo hello` through the proxy
	// returns the command's stdout, proving the HTTP-Upgrade exec channel passes
	// through end-to-end.
	t.Run("streaming_exec", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()

		group := uniqueName("grp")
		cluster := uniqueName("cl")
		ns := uniqueName("ns")
		createNamespace(t, ctx, admin, ns)
		pod := "exec-target"
		createPod(t, ctx, admin, ns, pod, []string{"sleep", "3600"})
		waitPodReady(t, ctx, admin, ns, pod, 3*time.Minute)

		grantClusterRole(t, ctx, admin, group, []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
			{APIGroups: []string{""}, Resources: []string{"pods/exec"}, Verbs: []string{"create"}},
		})
		enf, err := policy.New(ctx, "", nil)
		require.NoError(t, err)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{group}, ca.certPEM)
		kc := newKubectlRunner(t, ctx, addr, cluster, aliceCertPEM, aliceKeyPEM)

		var lastOut string
		require.Eventually(t, func() bool {
			out, code := kc.run("exec", pod, "-n", ns, "--", "echo", "hello")
			lastOut = out
			return code == 0 && strings.Contains(out, "hello")
		}, 90*time.Second, 2*time.Second, "kubectl exec through the proxy must stream 'hello': %s", truncateOutput(lastOut))
	})

	// (7) Streaming logs: `kubectl logs <pod>` through the proxy returns the
	// pod's log output.
	t.Run("streaming_logs", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()

		group := uniqueName("grp")
		cluster := uniqueName("cl")
		ns := uniqueName("ns")
		createNamespace(t, ctx, admin, ns)
		pod := "logs-target"
		createPod(t, ctx, admin, ns, pod, []string{"sh", "-c", "echo hello-logs; sleep 3600"})
		waitPodReady(t, ctx, admin, ns, pod, 3*time.Minute)

		grantClusterRole(t, ctx, admin, group, []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
			{APIGroups: []string{""}, Resources: []string{"pods/log"}, Verbs: []string{"get"}},
		})
		enf, err := policy.New(ctx, "", nil)
		require.NoError(t, err)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{group}, ca.certPEM)
		kc := newKubectlRunner(t, ctx, addr, cluster, aliceCertPEM, aliceKeyPEM)

		var lastOut string
		require.Eventually(t, func() bool {
			out, code := kc.run("logs", pod, "-n", ns)
			lastOut = out
			return code == 0 && strings.Contains(out, "hello-logs")
		}, 90*time.Second, 2*time.Second, "kubectl logs through the proxy must return the pod log: %s", truncateOutput(lastOut))
	})

	// (8) Audit allow: a successful kubectl get records an allow k8s_request
	// event with the actor, cluster, and parsed verb/resource.
	t.Run("audit_allow_recorded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		group := uniqueName("grp")
		cluster := uniqueName("cl")
		grantClusterRole(t, ctx, admin, group, []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		})
		enf, err := policy.New(ctx, "", nil)
		require.NoError(t, err)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{group}, ca.certPEM)
		kc := newKubectlRunner(t, ctx, addr, cluster, aliceCertPEM, aliceKeyPEM)

		require.Eventually(t, func() bool {
			_, code := kc.run("get", "pods", "-n", "default")
			return code == 0
		}, 30*time.Second, 2*time.Second, "kubectl get pods must succeed")

		require.True(t, auditMatch(sink.snapshot(), "allow", "alice", cluster, "list", "pods"),
			"expected an allow k8s_request audit event for list/pods, got: %+v", sink.snapshot())
	})

	// (9) Streaming port-forward: `kubectl port-forward` to a pod running a tiny
	// HTTP server, then curl the local end from inside the container — proving
	// the port-forward Upgrade channel passes through end-to-end.
	t.Run("streaming_portforward", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
		defer cancel()

		group := uniqueName("grp")
		cluster := uniqueName("cl")
		ns := uniqueName("ns")
		createNamespace(t, ctx, admin, ns)
		pod := "pf-target"
		createPod(t, ctx, admin, ns, pod,
			[]string{"sh", "-c", "mkdir -p /www && echo pf-ok > /www/index.html && httpd -f -p 8080 -h /www"})
		waitPodReady(t, ctx, admin, ns, pod, 3*time.Minute)

		grantClusterRole(t, ctx, admin, group, []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
			{APIGroups: []string{""}, Resources: []string{"pods/portforward"}, Verbs: []string{"create"}},
		})
		enf, err := policy.New(ctx, "", nil)
		require.NoError(t, err)
		sink := &captureSink{}
		addr := startProxy(t, adminRest, enf, sink, cluster, []string{group}, ca.certPEM)
		kc := newKubectlRunner(t, ctx, addr, cluster, aliceCertPEM, aliceKeyPEM)

		script := strings.ReplaceAll(strings.ReplaceAll(portForwardScript, "__POD__", pod), "__NS__", ns)
		var lastOut string
		require.Eventually(t, func() bool {
			out, _ := kc.runSh(script)
			lastOut = out
			return strings.Contains(out, "pf-ok")
		}, 120*time.Second, 3*time.Second, "kubectl port-forward through the proxy must serve pf-ok: %s", truncateOutput(lastOut))
	})
}

// ---- shared helpers (k8s_e2e only) ----

// startK3s runs ONE k3s container for the whole matrix and returns an admin
// rest.Config + clientset (k3s's default identity is cluster-admin, so it can
// impersonate any user/group). Skips (not fails) when Docker is unavailable.
//
// The container operations use a cancelable context that is cancelled on
// t.Cleanup before the container is terminated. That cancellation lets the
// testcontainers Docker client tear down its daemon keep-alive connection, so
// the package's goleak check (which runs only when the suite otherwise passes)
// does not flag a lingering readLoop/writeLoop goroutine — matching the sibling
// impersonation test, which is goleak-clean because it cancels its context.
func startK3s(t *testing.T) (*rest.Config, *kubernetes.Clientset) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	// Container lifetime spans the whole matrix. Registered first so it runs
	// LAST (after cancel below).
	container, err := k3s.Run(ctx, matrixK3sImage)
	if err != nil {
		cancel()
		t.Skipf("k3s/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate k3s container: %v", err)
		}
	})
	// Registered after terminate so it runs FIRST: cancel the container-ops
	// context, releasing the Docker client's idle connection before teardown.
	t.Cleanup(cancel)

	kubeBytes, err := container.GetKubeConfig(ctx)
	require.NoError(t, err)
	adminRest, err := clientcmd.RESTConfigFromKubeConfig(kubeBytes)
	require.NoError(t, err)
	admin, err := kubernetes.NewForConfig(adminRest)
	require.NoError(t, err)
	waitForAPIServer(t, admin, 90*time.Second)
	return adminRest, admin
}

// startProxy builds a single-cluster registry and runs the REAL production
// RunServer on a free 127.0.0.1 port with clientCAPEM as the mTLS client-CA
// trust anchor (the serving cert is self-signed under a temp dir). It returns
// the listen address; RunServer is cancelled + drained on t.Cleanup.
func startProxy(t *testing.T, adminRest *rest.Config, enf *policy.Enforcer, sink audit.Sink, clusterName string, defaultGroups []string, clientCAPEM []byte) string {
	t.Helper()

	stateDir := t.TempDir()
	reg, err := NewRegistry([]ClusterSpec{
		{Name: clusterName, Config: adminRest, UserFrom: "cn", DefaultGroups: defaultGroups},
	})
	require.NoError(t, err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	cfg := ServerConfig{
		Listen:    addr,
		Registry:  reg,
		Enforcer:  enf,
		AuditSink: sink,
		SANs:      []string{"127.0.0.1", "localhost"},
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- RunServer(runCtx, cfg, clientCAPEM, stateDir) }()
	t.Cleanup(func() {
		cancelRun()
		select {
		case err := <-errCh:
			if err != nil {
				t.Logf("RunServer(%s) returned: %v", clusterName, err)
			}
		case <-time.After(10 * time.Second):
			t.Logf("RunServer(%s) did not exit within 10s of cancellation", clusterName)
		}
	})

	require.Eventually(t, func() bool {
		conn, dialErr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if dialErr != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 100*time.Millisecond, "proxy server did not become ready")
	return addr
}

// kubectlRunner drives kubectl inside a single alpine/k8s container wired to one
// proxy over HostAccessPorts.
type kubectlRunner struct {
	// run executes `kubectl <args>` and returns combined output + exit code.
	run func(args ...string) (string, int)
	// runSh executes `sh -c <script>` and returns combined output + exit code
	// (used for the port-forward: background kubectl + wget).
	runSh func(script string) (string, int)
}

// newKubectlRunner starts ONE alpine/k8s container for the given proxy and
// returns a kubectlRunner. The kubeconfig targets
// https://host.testcontainers.internal:<port>/<cluster> with
// insecure-skip-tls-verify (the serving cert's SANs are 127.0.0.1/localhost, not
// the tunnel hostname); the mTLS CLIENT cert is presented and verified by the
// proxy, which is the property under test. A nil certPEM/keyPEM produces a
// kubeconfig with NO client certificate (for the no-cert rejection case).
func newKubectlRunner(t *testing.T, ctx context.Context, addr, cluster string, certPEM, keyPEM []byte) kubectlRunner {
	t.Helper()

	_, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	proxyPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	kcfg := api.NewConfig()
	clusterCfg := api.NewCluster()
	clusterCfg.Server = "https://" + testcontainers.HostInternal + ":" + strconv.Itoa(proxyPort) + "/" + cluster
	clusterCfg.InsecureSkipTLSVerify = true
	kcfg.Clusters["honey"] = clusterCfg

	authInfo := api.NewAuthInfo()
	if certPEM != nil && keyPEM != nil {
		authInfo.ClientCertificateData = certPEM
		authInfo.ClientKeyData = keyPEM
	} else {
		// No client cert. Set a placeholder bearer token so kubectl attempts the
		// (certless) TLS connection instead of prompting for interactive basic
		// auth; the mTLS handshake is rejected at the proxy before the token is
		// ever evaluated, which is the property under test.
		authInfo.Token = "no-client-cert"
	}
	kcfg.AuthInfos["client"] = authInfo

	kubeContext := api.NewContext()
	kubeContext.Cluster = "honey"
	kubeContext.AuthInfo = "client"
	kcfg.Contexts["honey"] = kubeContext
	kcfg.CurrentContext = "honey"

	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*kcfg, kubeconfigPath))

	req := testcontainers.ContainerRequest{
		Image:           matrixKubectlImage,
		HostAccessPorts: []int{proxyPort},
		Entrypoint:      []string{"sleep"},
		Cmd:             []string{"3600"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      kubeconfigPath,
			ContainerFilePath: "/root/.kube/config",
			FileMode:          0o600,
		}},
		WaitingFor: wait.ForExec([]string{"kubectl", "version", "--client"}).WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("kubectl container unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Logf("terminate kubectl container: %v", err)
		}
	})

	// tcexec.Multiplexed() strips Docker's stream-framing so the reader yields
	// plain combined stdout+stderr.
	exec := func(cmd []string) (string, int) {
		code, reader, execErr := c.Exec(ctx, cmd, tcexec.Multiplexed())
		if execErr != nil {
			return "container exec attach error: " + execErr.Error(), code
		}
		out, _ := io.ReadAll(reader)
		return string(out), code
	}
	return kubectlRunner{
		run:   func(args ...string) (string, int) { return exec(append([]string{"kubectl"}, args...)) },
		runSh: func(script string) (string, int) { return exec([]string{"sh", "-c", script}) },
	}
}

// parseSelfSubjectReview decodes the first JSON object in kubectl's output into a
// SelfSubjectReview (tolerating any leading/trailing non-JSON kubectl chatter).
func parseSelfSubjectReview(t *testing.T, out string) authenticationv1.SelfSubjectReview {
	t.Helper()
	idx := strings.IndexByte(out, '{')
	require.GreaterOrEqual(t, idx, 0, "no JSON object in kubectl output: %s", truncateOutput(out))
	var review authenticationv1.SelfSubjectReview
	dec := json.NewDecoder(strings.NewReader(out[idx:]))
	require.NoError(t, dec.Decode(&review), "parse SelfSubjectReview: %s", truncateOutput(out))
	return review
}

// tlsRejected reports whether kubectl's output looks like an mTLS handshake
// rejection (untrusted or absent client certificate).
func tlsRejected(out string) bool {
	low := strings.ToLower(out)
	for _, s := range []string{"tls", "certificate", "handshake", "x509", "remote error"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// enforcerFromRego writes src to a temp dir and builds a policy.Enforcer from it
// via policy.New (the production directory-loading path).
func enforcerFromRego(t *testing.T, ctx context.Context, src string) *policy.Enforcer {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, writeRego(dir, src))
	enf, err := policy.New(ctx, dir, nil)
	require.NoError(t, err)
	return enf
}

func writeRego(dir, src string) error {
	return os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(src), 0o600)
}

// grantClusterRole creates a uniquely-named ClusterRole with rules and binds it
// to group, cleaning both up on t.Cleanup. Cluster-scoped, so unique names keep
// subtests from interfering with one another.
func grantClusterRole(t *testing.T, ctx context.Context, admin *kubernetes.Clientset, group string, rules []rbacv1.PolicyRule) {
	t.Helper()
	crName := uniqueName("cr")
	crbName := uniqueName("crb")

	_, err := admin.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: crName},
		Rules:      rules,
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.RbacV1().ClusterRoles().Delete(context.Background(), crName, metav1.DeleteOptions{})
	})

	_, err = admin.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: crbName},
		Subjects:   []rbacv1.Subject{{Kind: "Group", Name: group, APIGroup: "rbac.authorization.k8s.io"}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: crName},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.RbacV1().ClusterRoleBindings().Delete(context.Background(), crbName, metav1.DeleteOptions{})
	})
}

// createNamespace creates ns, cleaning it up (best-effort) on t.Cleanup.
func createNamespace(t *testing.T, ctx context.Context, admin *kubernetes.Clientset, ns string) {
	t.Helper()
	_, err := admin.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = admin.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})
}

// createPod creates a single-container busybox pod running cmd in ns.
func createPod(t *testing.T, ctx context.Context, admin *kubernetes.Clientset, ns, name string, cmd []string) {
	t.Helper()
	_, err := admin.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "main",
				Image:   "busybox:1.36",
				Command: cmd,
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

// waitPodReady polls until the pod is Running with all containers ready.
func waitPodReady(t *testing.T, ctx context.Context, admin *kubernetes.Clientset, ns, name string, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		p, err := admin.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		if p.Status.Phase != corev1.PodRunning {
			return false
		}
		if len(p.Status.ContainerStatuses) == 0 {
			return false
		}
		for _, cs := range p.Status.ContainerStatuses {
			if !cs.Ready {
				return false
			}
		}
		return true
	}, timeout, 2*time.Second, "pod %s/%s did not become ready", ns, name)
}

// auditMatch reports whether events contains a k8s_request event matching the
// given decision, actor, cluster, verb, and resource.
func auditMatch(events []audit.Event, decision, actor, cluster, verb, resource string) bool {
	for _, e := range events {
		if e.Action == "k8s_request" &&
			e.Decision == decision &&
			e.Actor == actor &&
			e.Target == cluster &&
			e.Extra["verb"] == verb &&
			e.Extra["resource"] == resource {
			return true
		}
	}
	return false
}

// pemFromCert extracts PEM-encoded cert + EC private key from a tls.Certificate
// (the test client certs are all ECDSA/P-256).
func pemFromCert(t *testing.T, cert tls.Certificate) (certPEM, keyPEM []byte) {
	t.Helper()
	key, ok := cert.PrivateKey.(*ecdsa.PrivateKey)
	require.True(t, ok, "test client key must be ECDSA")
	keyDER, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// uniqueName builds a DNS-safe, collision-resistant name for cluster-scoped and
// namespaced objects created per subtest.
func uniqueName(prefix string) string {
	return "honey-" + prefix + "-" + k8srand.String(6)
}
