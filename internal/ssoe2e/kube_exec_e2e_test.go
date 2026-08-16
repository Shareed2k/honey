//go:build k8s_e2e

package ssoe2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientauthenticationv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"

	"github.com/shareed2k/honey/internal/oidc"
)

// execAPIVersion mirrors the unexported const of the same name in
// internal/cli/kube_exec.go: the client.authentication.k8s.io API version
// both writeExecKubeContext advertises in the kubeconfig's ExecConfig and
// emitExecCredential stamps on every ExecCredential it emits. Duplicated here
// (rather than imported) because this test lives in package ssoe2e, which
// cannot reach package cli's unexported identifiers — see the package doc
// comment on why (import-cycle avoidance forces the e2e harness into its own
// package).
const execAPIVersion = "client.authentication.k8s.io/v1"

// TestSSOE2E_KubeExecCredential proves the vertical between `honey kube
// login`'s SETUP mode and its kubectl exec-CREDENTIAL mode end to end: a
// signed client certificate obtained via the exact request setup mode sends
// (kubeOIDCLogin: POST /api/v1/kube/login with an id_token + CSR) — the same
// artifact setup mode caches (storeCachedCert) and credential mode later
// re-emits verbatim as an ExecCredential (emitExecCredential, see
// internal/cli/kube_exec.go) — is accepted by the REAL honey Kubernetes
// access proxy over mTLS.
//
// Why this test drives raw HTTP instead of calling runKubeLogin/
// runKubeCredential: those functions (and the browserAuthCodeFlowFn seam
// that stubs their browser leg in unit tests) live in package cli, which
// this package cannot import — see the package doc comment atop
// sso_e2e_test.go for why the e2e harness lives in its own package instead
// of alongside k8sproxy/cli. So this test reproduces, byte for byte, the two
// steps those functions perform:
//
//  1. fetchKubeCertViaSSO's request: generate an EC P-256 key + CSR in this
//     process (the private key never leaves it — only the CSR, which carries
//     just the public key, crosses the wire), obtain a real id_token via the
//     resource-owner-password-grant stand-in for the browser leg
//     (fetchIDToken, shared with TestSSOE2E_KubeAndSSH), and POST
//     {id_token, nonce, csr, cluster} to /api/v1/kube/login against the REAL
//     production webserver.Server + a REAL k3s cluster it fronts, exactly as
//     TestSSOE2E_KubeAndSSH's "kube" subtest and internal/cli's kubeOIDCLogin
//     do. The response (cn/groups/cert/proxy_ca) is what setup mode parses
//     and caches for credential mode to reuse.
//  2. emitExecCredential's envelope: build the identical
//     client.authentication.k8s.io/v1 ExecCredential struct emitExecCredential
//     constructs from a cachedCert (same TypeMeta, same
//     ClientCertificateData/ClientKeyData/ExpirationTimestamp fields), encode
//     it exactly as emitExecCredential does, then decode it back out —
//     reproducing byte for byte what kubectl parses from this plugin's
//     stdout on every API call.
//
// The proof: the cert+key recovered from THAT decoded ExecCredential build a
// client the real proxy authenticates and authorizes over mTLS — a real
// kubectl binary (via this package's newKubectlRunner, over testcontainers'
// HostAccessPorts tunnel) runs `auth whoami` and `get pods` successfully. A
// green run here proves the cert the exec plugin hands kubectl on every
// refresh is accepted by the proxy end to end.
//
// The private key is asserted absent from every artifact where its presence
// would be a leak: the setup-mode request's wire bytes, the login response,
// the server's audit trail, and kubectl's printed output. It is deliberately
// NOT asserted absent from the ExecCredential JSON itself — that document
// legitimately carries ClientKeyData (kubectl parses it from this plugin's
// stdout to build its own TLS transport); that is the entire point of the
// exec-credential protocol.
//
// Out of scope (covered elsewhere): the CLI wiring itself — flag parsing,
// writeExecKubeContext's exec-authInfo kubeconfig shape, storeCachedCert/
// loadCachedCert's on-disk cache, and runKubeCredential's freshness/
// interactive-mode branching — is unit-tested in package cli
// (internal/cli/kube_test.go, internal/cli/kube_exec_test.go), which is not
// reachable from this package.
func TestSSOE2E_KubeExecCredential(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// (1) Real k3s + admin clientset; RBAC granting the mapped group read
	// access to pods, same shape as TestSSOE2E_KubeAndSSH.
	adminRest, admin := startK3s(t)
	grantClusterRoleToGroup(t, admin, viewerGroup, []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get", "list", "watch"}},
	})

	// (2) Real OIDC provider; a verifier bound to its discovered issuer.
	issuer := startKeycloak(t)
	verifierCtx, cancelVerifier := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelVerifier()
	verifier, err := oidc.New(verifierCtx, oidc.Config{
		Issuer:        issuer,
		ClientID:      oidcClientID,
		UsernameClaim: "email",
		GroupsClaim:   "groups",
	})
	require.NoError(t, err, "build oidc verifier against issuer %s", issuer)

	// (3) One enforcer shared by the login endpoint AND the k8s proxy.
	enf := ssoEnforcer(t)

	// (4) The REAL production server: SSO login endpoints + the mTLS k8s proxy.
	sink := &captureSink{}
	apiAddr, proxyAddr := startServer(t, serverArgs{
		enf:       enf,
		verifier:  verifier,
		issuer:    issuer,
		adminRest: adminRest,
		sink:      sink,
	})

	// ---- Step 1: reproduce fetchKubeCertViaSSO's request ----

	// The in-test stand-in for the browser leg (no nonce), shared with
	// TestSSOE2E_KubeAndSSH.
	aliceToken := fetchIDToken(t, issuer, "alice", alicePassword)

	// generateKeyAndCSR's in-test equivalent: the key is generated here and
	// never sent anywhere.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	csrPEM := makeCSR(t, key, "honey-kube")
	keyPEM := ecKeyPEM(t, key)

	// kubeOIDCLogin's exact request shape.
	reqPayload := map[string]any{
		"id_token": aliceToken,
		"nonce":    "",
		"csr":      csrPEM,
		"cluster":  clusterName,
	}
	reqBody, err := json.Marshal(reqPayload)
	require.NoError(t, err)
	require.NotContains(t, string(reqBody), "PRIVATE KEY",
		"the setup-mode request must never carry key material, only the CSR's public key")

	resp, err := httpClient.Post("http://"+apiAddr+"/api/v1/kube/login", "application/json", bytes.NewReader(reqBody))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "kube/login: %s", truncate(string(respBody)))
	require.NotContains(t, string(respBody), "PRIVATE KEY",
		"the login response must never carry key material, only the signed certificate")

	var login kubeLoginResp
	require.NoError(t, json.Unmarshal(respBody, &login))
	require.Equal(t, aliceEmail, login.CN, "issued cert CN must be the mapped user")
	require.Equal(t, []string{viewerGroup}, login.Groups, "issued cert groups must be the mapped groups")
	require.NotEmpty(t, login.Cert, "a client certificate must be issued")
	require.NotEmpty(t, login.ProxyCA, "server must return its proxy serving CA")

	require.True(t, hasLoginAudit(sink.snapshot(), "kube_login", aliceEmail, "allow"),
		"expected an allow kube_login audit event for %s, got: %+v", aliceEmail, sink.snapshot())
	for _, e := range sink.snapshot() {
		blob, err := json.Marshal(e)
		require.NoError(t, err)
		require.NotContains(t, string(blob), "PRIVATE KEY",
			"the audit trail must never carry key material: %s", blob)
	}

	// ---- Step 2: reproduce emitExecCredential's envelope ----

	notAfter := parseCertNotAfter(t, []byte(login.Cert))
	execCred := clientauthenticationv1.ExecCredential{
		TypeMeta: metav1.TypeMeta{
			APIVersion: execAPIVersion,
			Kind:       "ExecCredential",
		},
		Status: &clientauthenticationv1.ExecCredentialStatus{
			ClientCertificateData: login.Cert,
			ClientKeyData:         string(keyPEM),
			ExpirationTimestamp:   &metav1.Time{Time: notAfter},
		},
	}
	// This encoded document DOES legitimately carry the key (see the doc
	// comment above) — it is the one artifact deliberately excluded from the
	// "never contains the key" assertions in this test.
	var execCredJSON bytes.Buffer
	require.NoError(t, json.NewEncoder(&execCredJSON).Encode(execCred))

	// Reproduce kubectl's side: parse exactly one ExecCredential document out
	// of this plugin's stdout.
	var decoded clientauthenticationv1.ExecCredential
	require.NoError(t, json.Unmarshal(execCredJSON.Bytes(), &decoded))
	require.Equal(t, execAPIVersion, decoded.APIVersion)
	require.Equal(t, "ExecCredential", decoded.Kind)
	require.NotNil(t, decoded.Status)
	require.NotEmpty(t, decoded.Status.ClientCertificateData)
	require.NotEmpty(t, decoded.Status.ClientKeyData)

	// ---- Step 3: the proof ----
	// The cert+key recovered from the DECODED ExecCredential (not the
	// originals kept around in this function) are what a real kubectl would
	// have received from this plugin's stdout; feed exactly those bytes to
	// the real proxy.
	run := newKubectlRunner(t, proxyAddr, clusterName,
		[]byte(decoded.Status.ClientCertificateData), []byte(decoded.Status.ClientKeyData))

	out, code := run("auth", "whoami", "-o", "json")
	require.Equal(t, 0, code, "kubectl auth whoami failed: %s", truncate(out))
	require.NotContains(t, out, "PRIVATE KEY", "kubectl output must never echo key material")
	review := parseSelfSubjectReview(t, out)
	require.Equal(t, aliceEmail, review.Status.UserInfo.Username,
		"the proxy must authenticate the exec-plugin-issued cert as the SSO-mapped user: %s", truncate(out))
	require.Contains(t, review.Status.UserInfo.Groups, viewerGroup,
		"the SSO-mapped group must be present: %s", truncate(out))

	require.Eventually(t, func() bool {
		out, code := run("get", "pods", "-n", "default")
		require.NotContains(t, out, "PRIVATE KEY", "kubectl output must never echo key material")
		return code == 0
	}, 30*time.Second, 2*time.Second, "kubectl get pods must succeed under the exec-plugin-issued cert")
}

// parseCertNotAfter mirrors package cli's unexported certNotAfter
// (internal/cli/kubecreds.go): decode the leaf certificate out of certPEM and
// return its NotAfter (expiry) time, as emitExecCredential's caller
// (runKubeCredential, via the cachedCert it loads or just stored) does when
// populating ExecCredentialStatus.ExpirationTimestamp.
func parseCertNotAfter(t *testing.T, certPEM []byte) time.Time {
	t.Helper()
	block, _ := pem.Decode(certPEM)
	require.NotNil(t, block, "cert PEM must decode")
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	return cert.NotAfter
}
