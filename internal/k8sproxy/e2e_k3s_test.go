//go:build k8s_e2e

// Package k8sproxy k8s_e2e test: exercises the proxy's Kubernetes
// impersonation against a REAL k3s cluster via testcontainers, driven through
// the REAL production entrypoint (RunServer + its mTLS listener) rather than a
// hand-built httptest server. Excluded from the normal `go test` run (and CI)
// by the k8s_e2e build tag — the rest of this package follows a no-real-cluster
// unit test convention. Requires a reachable Docker daemon; skips (rather than
// fails) when one isn't available. Run explicitly:
//
//	go test -tags k8s_e2e -run TestK8sProxyE2E_Impersonation -v ./internal/k8sproxy/ -timeout 15m
package k8sproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	authenticationv1 "k8s.io/api/authentication/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/shareed2k/honey/internal/policy"
)

// TestK8sProxyE2E_Impersonation drives real Kubernetes API calls through the
// REAL production proxy server (RunServer's mTLS listener, not a hand-built
// httptest harness) as a honey client, and proves the request is evaluated by
// the API server as the impersonated identity, not as the admin credentials
// the proxy authenticates to the cluster with:
//
//   - A SelfSubjectReview made through the proxy reports the impersonated
//     user/groups, not the k3s admin identity.
//   - A request the impersonated group is allowed (listing pods) succeeds.
//   - A request the impersonated group is NOT allowed (listing secrets) is
//     rejected as Forbidden, even though the proxy's own upstream
//     credentials are cluster-admin and could list secrets themselves.
//   - The real OPA gate + audit sink run in-line: at least one allow
//     "k8s_request" event is recorded for the impersonated actor.
func TestK8sProxyE2E_Impersonation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	container, err := k3s.Run(ctx, "rancher/k3s:v1.31.5-k3s1")
	if err != nil {
		t.Skipf("k3s/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate k3s container: %v", err)
		}
	})

	kubeBytes, err := container.GetKubeConfig(ctx)
	require.NoError(t, err)

	// This is the admin config: k3s's default kubeconfig identity is
	// cluster-admin, so it can impersonate any user/group.
	adminRest, err := clientcmd.RESTConfigFromKubeConfig(kubeBytes)
	require.NoError(t, err)

	adminClientset, err := kubernetes.NewForConfig(adminRest)
	require.NoError(t, err)
	waitForAPIServer(t, adminClientset, 90*time.Second)

	// Grant the impersonated group ("honey-viewers") read-only pod access —
	// and nothing else. This asymmetry (pods: yes, secrets: no) is what the
	// assertions below use to prove impersonation rather than admin
	// passthrough: the admin credentials backing the proxy COULD list
	// secrets; the impersonated group cannot.
	_, err = adminClientset.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "honey-pods-viewer"},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list"}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = adminClientset.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "honey-pods-viewer-binding"},
		Subjects: []rbacv1.Subject{
			{Kind: "Group", Name: "honey-viewers", APIGroup: "rbac.authorization.k8s.io"},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "honey-pods-viewer",
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	reg, err := NewRegistry([]ClusterSpec{
		{Name: "prod", Config: adminRest, UserFrom: "cn", DefaultGroups: []string{"honey-viewers"}},
	})
	require.NoError(t, err)

	// Front the real cluster with the REAL production server entrypoint
	// (RunServer's mTLS listener), the same way the webserver runs it in
	// production, rather than a hand-built httptest server.
	stateDir := t.TempDir()

	// EnsureServingCert mints (and persists) the proxy's serving keypair. Its
	// cert PEM doubles as the client's trust anchor (CAData) for the proxy's
	// TLS below.
	certPEM, keyPEM, err := EnsureServingCert(stateDir, []string{"127.0.0.1", "localhost"})
	require.NoError(t, err)
	crtPath := filepath.Join(stateDir, "serving.crt")
	keyPath := filepath.Join(stateDir, "serving.key")
	require.NoError(t, os.WriteFile(crtPath, certPEM, 0o644))
	require.NoError(t, os.WriteFile(keyPath, keyPEM, 0o600))

	// A real allow-enforcer (embedded default policy = allow) so the
	// k8s_request OPA gate actually runs, plus a capturing audit sink so we
	// can assert the gate + audit path executed for real.
	enf, err := policy.New(ctx, "", nil)
	require.NoError(t, err)
	sink := &captureSink{}

	// testCA signs the client cert "alice" presents; its cert PEM is handed to
	// RunServer as the default client-CA trust anchor so the proxy's mTLS
	// verifies alice's client cert.
	ca := newTestCA(t)

	// Pick a free port for the real listener (accept the tiny race; standard
	// in tests).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	cfg := ServerConfig{
		Listen:          addr,
		ServingCertPath: crtPath,
		ServingKeyPath:  keyPath,
		Registry:        reg,
		Enforcer:        enf,
		AuditSink:       sink,
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- RunServer(runCtx, cfg, ca.certPEM, stateDir)
	}()
	defer func() {
		cancelRun() // trigger RunServer's bounded-drain shutdown
		select {
		case err := <-serverErrCh:
			if err != nil {
				t.Logf("RunServer returned: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Log("RunServer did not exit within 10s of cancellation")
		}
	}()

	// Wait for the real listener to come up before dialing it for real.
	require.Eventually(t, func() bool {
		conn, dialErr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if dialErr != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 10*time.Second, 100*time.Millisecond, "proxy server did not become ready")

	// Build a client-go config that authenticates to the REAL proxy listener
	// as honey client "alice" and talks to the "prod" path prefix, exactly as
	// a real honey client (e.g. kubectl configured against the proxy) would.
	aliceCert := ca.clientCert(t)
	aliceKey, ok := aliceCert.PrivateKey.(*ecdsa.PrivateKey)
	require.True(t, ok, "test client key must be ECDSA")
	aliceKeyDER, err := x509.MarshalECPrivateKey(aliceKey)
	require.NoError(t, err)
	aliceCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: aliceCert.Certificate[0]})
	aliceKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: aliceKeyDER})

	proxyRest := &rest.Config{
		Host: "https://" + addr + "/prod",
		TLSClientConfig: rest.TLSClientConfig{
			CAData:   certPEM, // the proxy's serving cert is the client's trust anchor
			CertData: aliceCertPEM,
			KeyData:  aliceKeyPEM,
		},
	}
	proxyClient, err := kubernetes.NewForConfig(proxyRest)
	require.NoError(t, err)

	// (a) The API server itself reports the impersonated identity — proof
	// the proxy set Impersonate-User/-Group rather than passing its own
	// admin identity through.
	rev, err := proxyClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Equal(t, "alice", rev.Status.UserInfo.Username)
	require.Contains(t, rev.Status.UserInfo.Groups, "honey-viewers")

	// (b) Allowed: the impersonated group has get/list on pods. Wrapped in
	// Eventually since the API server's RBAC authorization cache can take a
	// moment to observe the ClusterRoleBinding created above.
	require.Eventually(t, func() bool {
		_, listErr := proxyClient.CoreV1().Pods("default").List(ctx, metav1.ListOptions{})
		return listErr == nil
	}, 20*time.Second, 500*time.Millisecond, "listing pods as the impersonated group must eventually succeed")

	// (c) Denied: the impersonated group has no secrets access. If the proxy
	// were forwarding under its own (cluster-admin) credentials instead of
	// impersonating "alice"/"honey-viewers", this would succeed.
	_, err = proxyClient.CoreV1().Secrets("default").List(ctx, metav1.ListOptions{})
	require.Error(t, err)
	require.True(t, apierrors.IsForbidden(err), "expected a Forbidden error, got: %v", err)

	// (d) The real OPA gate + audit sink ran in-line through RunServer: at
	// least one allow "k8s_request" event was recorded for alice/prod.
	events := sink.snapshot()
	found := false
	for _, e := range events {
		if e.Action == "k8s_request" && e.Actor == "alice" && e.Target == "prod" && e.Decision == "allow" {
			found = true
			break
		}
	}
	require.True(t, found, "expected an allow k8s_request audit event for alice/prod, got: %+v", events)

	// Beyond client-go, also drive a REAL kubectl binary through the same
	// proxy listener. client-go proves the wire protocol works; kubectl
	// additionally proves the proxy behaves correctly against kubectl's own
	// auth/RBAC UX (kubectl auth whoami, kubectl's Forbidden rendering),
	// which is how honey's actual users will hit this in practice. Skips
	// (does not fail) when kubectl isn't installed, since the client-go
	// assertions above already fully cover the impersonation contract.
	func() {
		kubectlPath, lookErr := exec.LookPath("kubectl")
		if lookErr != nil {
			t.Log("kubectl not installed; skipping kubectl assertions")
			return
		}
		if p := os.Getenv("KUBECTL"); p != "" {
			kubectlPath = p
		}
		t.Logf("using kubectl binary: %s", kubectlPath)

		// Write a real kubeconfig pointing at the proxy's "prod" path prefix,
		// trusting the proxy's serving cert and authenticating as alice —
		// exactly the config a real honey user would be handed.
		kcfg := api.NewConfig()
		cluster := api.NewCluster()
		cluster.Server = "https://" + addr + "/prod"
		cluster.CertificateAuthorityData = certPEM
		kcfg.Clusters["honey-prod"] = cluster

		authInfo := api.NewAuthInfo()
		authInfo.ClientCertificateData = aliceCertPEM
		authInfo.ClientKeyData = aliceKeyPEM
		kcfg.AuthInfos["honey-alice"] = authInfo

		kubeContext := api.NewContext()
		kubeContext.Cluster = "honey-prod"
		kubeContext.AuthInfo = "honey-alice"
		kcfg.Contexts["honey-prod"] = kubeContext
		kcfg.CurrentContext = "honey-prod"

		kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
		require.NoError(t, clientcmd.WriteToFile(*kcfg, kubeconfigPath))

		runKubectl := func(args ...string) (string, error) {
			cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			fullArgs := append([]string{"--kubeconfig", kubeconfigPath, "--context", "honey-prod"}, args...)
			out, runErr := exec.CommandContext(cmdCtx, kubectlPath, fullArgs...).CombinedOutput()
			return string(out), runErr
		}

		// (e) kubectl's own "auth whoami" is native proof the proxy
		// impersonates alice/honey-viewers, independent of client-go.
		whoamiOut, err := runKubectl("auth", "whoami", "-o", "json")
		require.NoError(t, err, "kubectl auth whoami failed: %s", truncateOutput(whoamiOut))
		var review authenticationv1.SelfSubjectReview
		require.NoError(t, json.Unmarshal([]byte(whoamiOut), &review), "parse kubectl auth whoami output: %s", truncateOutput(whoamiOut))
		require.Equal(t, "alice", review.Status.UserInfo.Username)
		require.Contains(t, review.Status.UserInfo.Groups, "honey-viewers")

		// (f) Allowed: kubectl can list pods as the impersonated group.
		// Eventually, to tolerate the same RBAC-cache lag as the client-go
		// pods check above.
		require.Eventually(t, func() bool {
			out, listErr := runKubectl("get", "pods", "-n", "default")
			if listErr != nil {
				t.Logf("kubectl get pods not yet allowed: %v: %s", listErr, truncateOutput(out))
				return false
			}
			return true
		}, 15*time.Second, 500*time.Millisecond, "kubectl get pods as the impersonated group must eventually succeed")

		// (g) Denied: kubectl requests run as the impersonated identity, not
		// the proxy's admin credentials — secrets must fail as Forbidden.
		secretsOut, err := runKubectl("get", "secrets", "-n", "default")
		require.Error(t, err, "kubectl get secrets unexpectedly succeeded: %s", truncateOutput(secretsOut))
		require.Contains(t, strings.ToLower(secretsOut), "forbidden", "expected forbidden in kubectl output: %s", truncateOutput(secretsOut))
	}()
}

// truncateOutput bounds command output for test log/failure messages so a
// runaway kubectl error doesn't flood test output.
func truncateOutput(s string) string {
	const max = 2000
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// waitForAPIServer polls the k8s API server up to timeout; k3s can take a
// moment after Run returns before its control plane accepts requests.
func waitForAPIServer(t *testing.T, clientset *kubernetes.Clientset, timeout time.Duration) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := clientset.Discovery().ServerVersion()
		return err == nil
	}, timeout, 2*time.Second, "k8s API server did not become ready")
}
