//go:build k8s_e2e

// Package k8sproxy k8s_e2e test: exercises the proxy's Kubernetes
// impersonation against a REAL k3s cluster via testcontainers. Excluded from
// the normal `go test` run (and CI) by the k8s_e2e build tag — the rest of
// this package follows a no-real-cluster unit test convention. Requires a
// reachable Docker daemon; skips (rather than fails) when one isn't
// available. Run explicitly:
//
//	go test -tags k8s_e2e -run TestK8sProxyE2E_Impersonation -v ./internal/k8sproxy/ -timeout 15m
package k8sproxy

import (
	"context"
	"crypto/ecdsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
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
)

// TestK8sProxyE2E_Impersonation drives real Kubernetes API calls through the
// proxy as a honey client and proves the request is evaluated by the API
// server as the impersonated identity, not as the admin credentials the
// proxy authenticates to the cluster with:
//
//   - A SelfSubjectReview made through the proxy reports the impersonated
//     user/groups, not the k3s admin identity.
//   - A request the impersonated group is allowed (listing pods) succeeds.
//   - A request the impersonated group is NOT allowed (listing secrets) is
//     rejected as Forbidden, even though the proxy's own upstream
//     credentials are cluster-admin and could list secrets themselves.
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

	// Front the real cluster with the proxy over mTLS, the same way it's set
	// up in the mTLS harness in handler_test.go.
	reg, err := NewRegistry([]ClusterSpec{
		{Name: "prod", Config: adminRest, UserFrom: "cn", DefaultGroups: []string{"honey-viewers"}},
	})
	require.NoError(t, err)

	proxyHandler := NewHandler(reg, nil, nil)

	ca := newTestCA(t)
	clientCAPool := x509.NewCertPool()
	require.True(t, clientCAPool.AppendCertsFromPEM(ca.certPEM))

	front := httptest.NewUnstartedServer(proxyHandler)
	front.TLS = &tls.Config{
		ClientCAs:  clientCAPool,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
	}
	front.StartTLS()
	defer front.Close()

	// Build a client-go config that authenticates to the proxy as honey
	// client "alice" and talks to the "prod" path prefix, exactly as a real
	// honey client (e.g. kubectl configured against the proxy) would.
	aliceCert := ca.clientCert(t)
	aliceKey, ok := aliceCert.PrivateKey.(*ecdsa.PrivateKey)
	require.True(t, ok, "test client key must be ECDSA")
	aliceKeyDER, err := x509.MarshalECPrivateKey(aliceKey)
	require.NoError(t, err)

	proxyRest := &rest.Config{
		Host: front.URL + "/prod",
		TLSClientConfig: rest.TLSClientConfig{
			CAData:   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: front.Certificate().Raw}),
			CertData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: aliceCert.Certificate[0]}),
			KeyData:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: aliceKeyDER}),
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
