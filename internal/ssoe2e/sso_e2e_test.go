//go:build k8s_e2e

// Package ssoe2e is an end-to-end proof of honey's single-sign-on identity for
// the access gateways, wired exactly as production wires it: one
// webserver.Server hosts BOTH the SSO login endpoints (plaintext API) AND the
// Kubernetes access proxy (mTLS), sharing ONE OPA enforcer and ONE device CA.
//
// A REAL corporate identity provider (an OIDC server run as a throwaway
// container) issues id_tokens; a REAL k3s cluster is fronted by the proxy. The
// login endpoints verify the id_token, an OPA `identity` policy maps its claims
// to a Kubernetes user+groups / ssh principals, and honey issues a short-lived
// certificate the gateway consumes. Every kube assertion is proven with a REAL
// kubectl binary run inside an alpine/k8s container reaching the host-side proxy
// over testcontainers' HostAccessPorts tunnel — the proof is kubectl's exit code
// and output, not an in-process client.
//
// This package deliberately imports BOTH internal/k8sproxy and
// internal/webserver: k8sproxy is imported by webserver, so a test living in
// k8sproxy that imported webserver would create an import cycle; a fresh package
// importing both does not.
//
// Excluded from the normal `go test` run (and CI unit runs) by the k8s_e2e build
// tag. Requires a reachable Docker daemon; the ONLY skips are the two
// environment gates (Docker-unavailable when starting a container). Run
// explicitly:
//
//	go test -tags k8s_e2e -run TestSSOE2E ./internal/ssoe2e/ -v -timeout 40m
package ssoe2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/goleak"
	"golang.org/x/crypto/ssh"
	authenticationv1 "k8s.io/api/authentication/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/k8sproxy"
	"github.com/shareed2k/honey/internal/oidc"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/sshca"
	"github.com/shareed2k/honey/internal/webserver"
)

const (
	// keycloakImage is the throwaway OIDC identity provider. Only used as a test
	// image name (like rancher/k3s), never referenced by honey non-test code.
	keycloakImage = "quay.io/keycloak/keycloak:26.0"
	// k3sImage is the upstream Kubernetes distribution the proxy fronts.
	k3sImage = "rancher/k3s:v1.31.5-k3s1"
	// kubectlImage bundles kubectl + a shell so every kube assertion is proven
	// with the real client reaching the proxy over HostAccessPorts.
	kubectlImage = "alpine/k8s:1.31.7"

	realmName    = "corp"
	oidcClientID = "honey-kube"
	viewerGroup  = "honey-viewers"
	clusterName  = "sso-cluster"

	// Passwords for the imported realm users. Non-secret test fixtures.
	alicePassword = "alice-e2e-pw"
	bobPassword   = "bob-e2e-pw"

	// aliceEmail / bobEmail are the email claims the identity policy keys on.
	aliceEmail = "alice@corp"
	bobEmail   = "bob@corp"
)

// realmTemplate is the realm-import JSON. Placeholders %s are alice's then bob's
// password. It defines: realm "corp"; a public client "honey-kube" with the
// standard + direct-access-grant flows and loopback redirect URIs, carrying an
// inline group-membership mapper that always puts a (short-path) "groups" claim
// into the id token (a client-attached mapper, so the claim is present for any
// requested scope); a realm group "eng"; user "alice" (in eng) and user "bob"
// (not in eng), both fully populated (email, verified, first/last name so the
// realm's user-profile requirements are satisfied and the direct grant is not
// blocked by a pending "update profile" action) with a set password.
// sslRequired "none" so the container is reachable over plain http on the mapped
// port. Note: the realm intentionally does NOT declare a top-level clientScopes
// array — doing so would REPLACE Keycloak's built-in scopes (email, profile, …)
// and make "email" an invalid scope to request.
const realmTemplate = `{
  "realm": "corp",
  "enabled": true,
  "sslRequired": "none",
  "clients": [
    {
      "clientId": "honey-kube",
      "enabled": true,
      "protocol": "openid-connect",
      "publicClient": true,
      "standardFlowEnabled": true,
      "directAccessGrantsEnabled": true,
      "redirectUris": ["http://127.0.0.1:*/callback", "*"],
      "webOrigins": ["*"],
      "fullScopeAllowed": true,
      "protocolMappers": [
        {
          "name": "groups",
          "protocol": "openid-connect",
          "protocolMapper": "oidc-group-membership-mapper",
          "consentRequired": false,
          "config": {
            "full.path": "false",
            "id.token.claim": "true",
            "access.token.claim": "true",
            "userinfo.token.claim": "true",
            "claim.name": "groups"
          }
        }
      ]
    }
  ],
  "groups": [
    { "name": "eng" }
  ],
  "users": [
    {
      "username": "alice",
      "enabled": true,
      "email": "alice@corp",
      "emailVerified": true,
      "firstName": "Alice",
      "lastName": "Example",
      "credentials": [{ "type": "password", "value": "%s", "temporary": false }],
      "groups": ["/eng"]
    },
    {
      "username": "bob",
      "enabled": true,
      "email": "bob@corp",
      "emailVerified": true,
      "firstName": "Bob",
      "lastName": "Example",
      "credentials": [{ "type": "password", "value": "%s", "temporary": false }],
      "groups": []
    }
  ]
}`

// ssoPolicy is one OPA module (package honey) carrying BOTH policies the feature
// needs. identity: an SSO subject in group "eng" maps to a honey k8s user (their
// email), groups [honey-viewers], and ssh principals [ubuntu, email]; a subject
// with no eng membership resolves no identity, so login is denied fail-closed.
// k8s_request: honey-viewers may act on any resource EXCEPT secrets (secrets are
// explicitly denied at the boundary); the cluster's RBAC further constrains the
// group to reading pods, so get/list pods succeeds while get secrets is refused.
const ssoPolicy = `package honey

import rego.v1

default allow := false

identity := {
	"user": input.email,
	"groups": ["honey-viewers"],
	"principals": ["ubuntu", input.email],
} if {
	input.action == "identity"
	"eng" in input.groups
}

allow if {
	input.action == "identity"
	identity
}

allow if {
	input.action == "k8s_request"
	"honey-viewers" in input.groups
	input.resource != "secrets"
}
`

// httpClient is a keep-alive-free client for the test's direct HTTP calls (token
// fetch, login). DisableKeepAlives keeps no idle-connection goroutines alive, so
// the package's goleak check stays clean.
var httpClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{DisableKeepAlives: true},
}

// TestMain runs the package under goleak. Every container context is cancelled
// before its container is terminated and the server context is cancelled and
// drained, so the test itself leaks nothing. The honey webserver and engine do,
// however, start long-lived background goroutines that outlive a single server
// (some are process-global singletons): the webhook queue's worker-pool
// janitors (panjf2000/ants), the engine's global tunnel-pool sweeper, and the
// recipes API's ttl-cache janitor. The webserver package's own goleak tests
// exclude these the same way (via IgnoreCurrent); they are named explicitly here
// so a genuine leak introduced by THIS test still fails the check.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).purgeStaleWorkers"),
		goleak.IgnoreTopFunction("github.com/panjf2000/ants/v2.(*poolCommon).ticktock"),
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"),
		goleak.IgnoreTopFunction("github.com/jellydator/ttlcache/v3.(*Cache[...]).Start"),
	)
}

// TestSSOE2E_KubeAndSSH proves the whole SSO login path live against a real IdP
// and a real k3s cluster: kube login → kubectl through the mTLS proxy; ssh login
// → a verifiable OpenSSH user certificate; and a non-member is denied.
func TestSSOE2E_KubeAndSSH(t *testing.T) {
	// A dedicated state dir so NewServer creates the device CA + ssh CA and the
	// proxy serving cert under a temp dir (config.ResolveStateDir reads
	// XDG_STATE_HOME). Set before NewServer so the CAs land here.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stateDir, err := config.ResolveStateDir()
	require.NoError(t, err)

	// (1) Real k3s + admin clientset; RBAC granting the mapped group read access
	// to pods (bound to the GROUP, matching honey's impersonated identity).
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

	// (3) One enforcer shared by the login endpoints AND the k8s proxy.
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

	// (5) The in-test stand-in for the browser leg: a resource-owner password
	// grant yields alice's id_token (carrying no nonce).
	aliceToken := fetchIDToken(t, issuer, "alice", alicePassword)

	t.Run("kube", func(t *testing.T) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		csrPEM := makeCSR(t, key, "sso-login")
		keyPEM := ecKeyPEM(t, key)

		login := kubeLogin(t, apiAddr, aliceToken, clusterName, csrPEM)
		require.Equal(t, aliceEmail, login.CN, "issued cert CN must be the mapped user")
		require.Equal(t, []string{viewerGroup}, login.Groups, "issued cert groups must be the mapped groups")
		require.NotEmpty(t, login.Cert, "a client certificate must be issued")

		run := newKubectlRunner(t, proxyAddr, clusterName, []byte(login.Cert), keyPEM)

		// auth whoami: the proxy impersonates the SSO-mapped identity.
		out, code := run("auth", "whoami", "-o", "json")
		require.Equal(t, 0, code, "kubectl auth whoami failed: %s", truncate(out))
		review := parseSelfSubjectReview(t, out)
		require.Equal(t, aliceEmail, review.Status.UserInfo.Username,
			"proxy must impersonate the SSO user: %s", truncate(out))
		require.Contains(t, review.Status.UserInfo.Groups, viewerGroup,
			"the SSO-mapped group must be present: %s", truncate(out))

		// get pods: allowed by both the OPA gate and RBAC.
		require.Eventually(t, func() bool {
			_, code := run("get", "pods", "-n", "default")
			return code == 0
		}, 30*time.Second, 2*time.Second, "kubectl get pods must succeed under the SSO identity")

		// get secrets: denied by the OPA gate at the boundary.
		out, code = run("get", "secrets", "-n", "default")
		require.NotEqual(t, 0, code, "kubectl get secrets must be denied: %s", truncate(out))
		require.Contains(t, strings.ToLower(out), "forbidden",
			"get secrets must be forbidden: %s", truncate(out))

		require.True(t, hasLoginAudit(sink.snapshot(), "kube_login", aliceEmail, "allow"),
			"expected an allow kube_login audit event for %s, got: %+v", aliceEmail, sink.snapshot())
	})

	t.Run("ssh", func(t *testing.T) {
		pub, _, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		sshPub, err := ssh.NewPublicKey(pub)
		require.NoError(t, err)
		authorizedKey := string(ssh.MarshalAuthorizedKey(sshPub))

		login := sshLogin(t, apiAddr, aliceToken, authorizedKey)
		require.Equal(t, aliceEmail, login.CN)
		require.ElementsMatch(t, []string{"ubuntu", aliceEmail}, login.Principals)

		// The issued cert parses as an OpenSSH user certificate.
		parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(login.Cert))
		require.NoError(t, err, "returned ssh cert must parse")
		cert, ok := parsed.(*ssh.Certificate)
		require.True(t, ok, "returned key must be a certificate")
		require.Equal(t, uint32(ssh.UserCert), cert.CertType)
		require.Equal(t, aliceEmail, cert.KeyId)
		require.Contains(t, cert.ValidPrincipals, "ubuntu")
		require.Contains(t, cert.ValidPrincipals, aliceEmail)

		// Strongest structural check without booting the gateway: a CertChecker
		// trusting honey's own ssh CA (read from the state dir the server minted
		// it under) accepts the cert for a permitted principal.
		caPub, present, err := sshca.LoadCAPublicKey(stateDir)
		require.NoError(t, err)
		require.True(t, present, "the server must have created an ssh CA under the state dir")
		checker := &ssh.CertChecker{
			IsUserAuthority: func(auth ssh.PublicKey) bool {
				return bytes.Equal(auth.Marshal(), caPub.Marshal())
			},
		}
		require.NoError(t, checker.CheckCert("ubuntu", cert),
			"the honey ssh CA must be accepted as the cert's authority for principal ubuntu")

		require.True(t, hasLoginAudit(sink.snapshot(), "ssh_login", aliceEmail, "allow"),
			"expected an allow ssh_login audit event for %s, got: %+v", aliceEmail, sink.snapshot())
	})

	t.Run("deny", func(t *testing.T) {
		// bob is not in group eng, so the identity policy resolves no identity.
		bobToken := fetchIDToken(t, issuer, "bob", bobPassword)

		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		require.NoError(t, err)
		csrPEM := makeCSR(t, key, "sso-login")

		status, body := postJSON(t, "http://"+apiAddr+"/api/v1/kube/login", map[string]any{
			"id_token": bobToken,
			"nonce":    "",
			"csr":      csrPEM,
			"cluster":  clusterName,
		})
		require.Equal(t, http.StatusForbidden, status,
			"a subject with no identity mapping must be denied: %s", truncate(body))
		require.NotContains(t, body, "-----BEGIN CERTIFICATE-----",
			"a denied login must never return a certificate")
		require.True(t, hasLoginAudit(sink.snapshot(), "kube_login", bobEmail, "deny"),
			"expected a deny kube_login audit event for %s, got: %+v", bobEmail, sink.snapshot())
	})
}

// ---- server boot ----

// serverArgs bundles the dependencies startServer wires into webserver.Options.
type serverArgs struct {
	enf       *policy.Enforcer
	verifier  *oidc.Verifier
	issuer    string
	adminRest *rest.Config
	sink      audit.Sink

	// interceptBroker and interceptModes are optional: nil/empty leaves the
	// intercept routes unregistered (webserver.Options.InterceptBroker nil),
	// so TestSSOE2E_KubeAndSSH — which never sets these — is unaffected.
	interceptBroker *intercept.Broker
	interceptModes  []string
}

// startServer boots ONE real webserver.Server hosting the SSO login endpoints
// on a plaintext API listener AND the k8s access proxy on an mTLS listener (its
// client-CA trust anchor is the device CA the server minted under the state dir,
// which is the same CA the kube-login handler signs with). It returns the two
// listen addresses once both accept; the server is cancelled and drained on
// t.Cleanup.
func startServer(t *testing.T, args serverArgs) (apiAddr, proxyAddr string) {
	t.Helper()

	reg, err := k8sproxy.NewRegistry([]k8sproxy.ClusterSpec{{
		Name:     clusterName,
		Config:   args.adminRest,
		UserFrom: "cn",
		Labels:   map[string]string{"env": "dev"},
	}})
	require.NoError(t, err)

	apiAddr = pickAddr(t)
	proxyAddr = pickAddr(t)

	srv, err := webserver.NewServer(webserver.Options{
		ListenAddr:           apiAddr,
		Token:                "sso-e2e-token",
		AuditSink:            args.sink,
		Enforcer:             args.enf,
		OIDCVerifier:         args.verifier,
		OIDCPublic:           &webserver.OIDCPublicConfig{Issuer: args.issuer, ClientID: oidcClientID, Scopes: []string{"openid", "email"}},
		DeviceCertTTL:        time.Hour,
		InterceptBroker:      args.interceptBroker,
		InterceptDefaultMode: args.interceptModes,
		K8sProxy: &k8sproxy.ServerConfig{
			Listen:    proxyAddr,
			Registry:  reg,
			Enforcer:  args.enf,
			AuditSink: args.sink,
			SANs:      []string{"127.0.0.1", "localhost"},
		},
	})
	require.NoError(t, err)

	srvCtx, cancelSrv := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start(srvCtx) }()
	t.Cleanup(func() {
		cancelSrv()
		select {
		case err := <-errCh:
			if err != nil {
				t.Logf("server Start returned: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Logf("server did not exit within 15s of cancellation")
		}
	})

	waitForTCP(t, apiAddr)
	waitForTCP(t, proxyAddr)
	return apiAddr, proxyAddr
}

// ssoEnforcer writes ssoPolicy to a temp dir and builds a policy.Enforcer from
// it via the production directory-loading path (policy.New).
func ssoEnforcer(t *testing.T) *policy.Enforcer {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sso.rego"), []byte(ssoPolicy), 0o600))
	enf, err := policy.New(context.Background(), dir, nil)
	require.NoError(t, err)
	return enf
}

// ---- containers ----

// startK3s runs ONE k3s container and returns an admin rest.Config + clientset
// (k3s's default identity is cluster-admin, so it can impersonate any
// user/group). Skips when Docker is unavailable. The container-ops context is
// cancelled before teardown so the Docker client's keep-alive goroutines exit
// (goleak stays clean).
func startK3s(t *testing.T) (*rest.Config, *kubernetes.Clientset) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	container, err := k3s.Run(ctx, k3sImage)
	if err != nil {
		cancel()
		t.Skipf("k3s/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate k3s container: %v", err)
		}
	})
	t.Cleanup(cancel)

	kubeBytes, err := container.GetKubeConfig(ctx)
	require.NoError(t, err)
	adminRest, err := clientcmd.RESTConfigFromKubeConfig(kubeBytes)
	require.NoError(t, err)
	admin, err := kubernetes.NewForConfig(adminRest)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := admin.Discovery().ServerVersion()
		return err == nil
	}, 90*time.Second, 2*time.Second, "k8s API server did not become ready")
	return adminRest, admin
}

// startKeycloak runs the OIDC provider in dev mode importing the realm, waits
// for the realm's discovery endpoint to serve, and returns the issuer URL
// (host+mapped-port derived, so it matches the iss claim the container mints).
func startKeycloak(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	realmPath := filepath.Join(t.TempDir(), "realm.json")
	require.NoError(t, os.WriteFile(realmPath, []byte(fmt.Sprintf(realmTemplate, alicePassword, bobPassword)), 0o600))

	req := testcontainers.ContainerRequest{
		Image: keycloakImage,
		Env: map[string]string{
			"KC_BOOTSTRAP_ADMIN_USERNAME": "admin",
			"KC_BOOTSTRAP_ADMIN_PASSWORD": "admin",
		},
		Cmd:          []string{"start-dev", "--import-realm"},
		ExposedPorts: []string{"8080/tcp"},
		Files: []testcontainers.ContainerFile{{
			HostFilePath:      realmPath,
			ContainerFilePath: "/opt/keycloak/data/import/realm.json",
			FileMode:          0o644,
		}},
		WaitingFor: wait.ForHTTP("/realms/" + realmName + "/.well-known/openid-configuration").
			WithPort("8080/tcp").
			WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
			WithStartupTimeout(180 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		cancel()
		t.Skipf("keycloak/testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Logf("terminate keycloak container: %v", err)
		}
	})
	t.Cleanup(cancel)

	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "8080/tcp")
	require.NoError(t, err)
	return fmt.Sprintf("http://%s:%s/realms/%s", host, port.Port(), realmName)
}

// kubectlRun executes `kubectl <args>` inside the runner's container and returns
// combined output + exit code.
type kubectlRun func(args ...string) (string, int)

// newKubectlRunner starts ONE alpine/k8s container wired to the proxy over
// HostAccessPorts and returns a runner. The kubeconfig targets
// https://host.testcontainers.internal:<proxyPort>/<cluster> with
// insecure-skip-tls-verify (the serving cert's SANs are 127.0.0.1/localhost, not
// the tunnel hostname); the mTLS CLIENT cert (the SSO-issued cert) is presented
// and verified by the proxy, which is the property under test.
func newKubectlRunner(t *testing.T, proxyAddr, cluster string, certPEM, keyPEM []byte) kubectlRun {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	_, portStr, err := net.SplitHostPort(proxyAddr)
	require.NoError(t, err)
	proxyPort, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	kcfg := api.NewConfig()
	clusterCfg := api.NewCluster()
	clusterCfg.Server = "https://" + testcontainers.HostInternal + ":" + strconv.Itoa(proxyPort) + "/" + cluster
	clusterCfg.InsecureSkipTLSVerify = true
	kcfg.Clusters["honey"] = clusterCfg

	authInfo := api.NewAuthInfo()
	authInfo.ClientCertificateData = certPEM
	authInfo.ClientKeyData = keyPEM
	kcfg.AuthInfos["client"] = authInfo

	kubeContext := api.NewContext()
	kubeContext.Cluster = "honey"
	kubeContext.AuthInfo = "client"
	kcfg.Contexts["honey"] = kubeContext
	kcfg.CurrentContext = "honey"

	kubeconfigPath := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, clientcmd.WriteToFile(*kcfg, kubeconfigPath))

	req := testcontainers.ContainerRequest{
		Image:           kubectlImage,
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
		cancel()
		t.Skipf("kubectl container unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Logf("terminate kubectl container: %v", err)
		}
	})
	t.Cleanup(cancel)

	return func(args ...string) (string, int) {
		cmd := append([]string{"kubectl"}, args...)
		code, reader, execErr := c.Exec(ctx, cmd, tcexec.Multiplexed())
		if execErr != nil {
			return "container exec attach error: " + execErr.Error(), code
		}
		out, _ := io.ReadAll(reader)
		return string(out), code
	}
}

// ---- HTTP helpers ----

// fetchIDToken performs a resource-owner password grant against the issuer's
// token endpoint and returns the id_token — the in-test stand-in for the browser
// sign-in leg (these tokens carry no nonce).
func fetchIDToken(t *testing.T, issuer, username, password string) string {
	t.Helper()
	form := url.Values{}
	form.Set("grant_type", "password")
	form.Set("client_id", oidcClientID)
	form.Set("username", username)
	form.Set("password", password)
	// "openid email" are valid scopes to request; the groups claim is delivered
	// by the client-attached mapper (always active), not a requestable scope —
	// Keycloak rejects requesting an unregistered "groups" scope.
	form.Set("scope", "openid email")

	resp, err := httpClient.PostForm(issuer+"/protocol/openid-connect/token", form)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	require.Equal(t, http.StatusOK, resp.StatusCode, "token endpoint for %s: %s", username, truncate(string(body)))

	var tok struct {
		IDToken string `json:"id_token"`
	}
	require.NoError(t, json.Unmarshal(body, &tok))
	require.NotEmpty(t, tok.IDToken, "token response carried no id_token: %s", truncate(string(body)))
	return tok.IDToken
}

// kubeLoginResp is the /api/v1/kube/login response.
type kubeLoginResp struct {
	CN      string   `json:"cn"`
	Groups  []string `json:"groups"`
	Cert    string   `json:"cert"`
	ProxyCA string   `json:"proxy_ca"`
}

// kubeLogin posts a kube-login request and requires a 200, returning the parsed
// response.
func kubeLogin(t *testing.T, apiAddr, idToken, cluster, csrPEM string) kubeLoginResp {
	t.Helper()
	status, body := postJSON(t, "http://"+apiAddr+"/api/v1/kube/login", map[string]any{
		"id_token": idToken,
		"nonce":    "",
		"csr":      csrPEM,
		"cluster":  cluster,
	})
	require.Equal(t, http.StatusOK, status, "kube/login: %s", truncate(body))
	var out kubeLoginResp
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	return out
}

// sshLoginResp is the /api/v1/ssh/login response.
type sshLoginResp struct {
	CN         string   `json:"cn"`
	Principals []string `json:"principals"`
	Cert       string   `json:"cert"`
}

// sshLogin posts an ssh-login request and requires a 200, returning the parsed
// response.
func sshLogin(t *testing.T, apiAddr, idToken, publicKey string) sshLoginResp {
	t.Helper()
	status, body := postJSON(t, "http://"+apiAddr+"/api/v1/ssh/login", map[string]any{
		"id_token":   idToken,
		"nonce":      "",
		"public_key": publicKey,
	})
	require.Equal(t, http.StatusOK, status, "ssh/login: %s", truncate(body))
	var out sshLoginResp
	require.NoError(t, json.Unmarshal([]byte(body), &out))
	return out
}

// postJSON posts v as JSON and returns the status code + response body.
func postJSON(t *testing.T, urlStr string, v any) (int, string) {
	t.Helper()
	payload, err := json.Marshal(v)
	require.NoError(t, err)
	resp, err := httpClient.Post(urlStr, "application/json", bytes.NewReader(payload))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// ---- crypto + parsing helpers ----

// makeCSR builds a PEM-encoded PKCS#10 certificate request for key with cn (the
// login handler overrides the CN with the SSO-mapped user, so cn is cosmetic).
func makeCSR(t *testing.T, key *ecdsa.PrivateKey, cn string) string {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}, key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

// ecKeyPEM encodes an EC private key as a SEC1 "EC PRIVATE KEY" PEM block (the
// form kubectl expects alongside the issued client certificate).
func ecKeyPEM(t *testing.T, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// parseSelfSubjectReview decodes the first JSON object in kubectl's output into a
// SelfSubjectReview (tolerating leading/trailing kubectl chatter).
func parseSelfSubjectReview(t *testing.T, out string) authenticationv1.SelfSubjectReview {
	t.Helper()
	idx := strings.IndexByte(out, '{')
	require.GreaterOrEqual(t, idx, 0, "no JSON object in kubectl output: %s", truncate(out))
	var review authenticationv1.SelfSubjectReview
	dec := json.NewDecoder(strings.NewReader(out[idx:]))
	require.NoError(t, dec.Decode(&review), "parse SelfSubjectReview: %s", truncate(out))
	return review
}

// ---- admin fixtures ----

// grantClusterRoleToGroup creates a ClusterRole with rules bound to group,
// cleaning both up on t.Cleanup. Binding to the GROUP (not the user) matches
// honey's impersonation: the SSO cert carries the group in its O= fields.
func grantClusterRoleToGroup(t *testing.T, admin *kubernetes.Clientset, group string, rules []rbacv1.PolicyRule) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	crName := "honey-sso-cr"
	crbName := "honey-sso-crb"

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

// ---- audit sink ----

// captureSink is an in-memory audit.Sink recording every event for assertions.
type captureSink struct {
	mu     sync.Mutex
	events []audit.Event
}

// Log records e.
func (s *captureSink) Log(_ context.Context, e audit.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

// Close is a no-op.
func (s *captureSink) Close() error { return nil }

// snapshot returns a copy of the recorded events.
func (s *captureSink) snapshot() []audit.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.Event(nil), s.events...)
}

// hasLoginAudit reports whether events contains a login event with the given
// action ("kube_login"/"ssh_login"), actor, and decision.
func hasLoginAudit(events []audit.Event, action, actor, decision string) bool {
	for _, e := range events {
		if e.Action == action && e.Actor == actor && e.Decision == decision {
			return true
		}
	}
	return false
}

// ---- misc ----

// pickAddr binds an ephemeral 127.0.0.1 port, closes it, and returns the address
// so a server can rebind it (the tiny race is acceptable in a test).
func pickAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// waitForTCP polls until addr accepts a TCP connection.
func waitForTCP(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 15*time.Second, 100*time.Millisecond, "listener %s did not become ready", addr)
}

// truncate bounds long kubectl/HTTP output in failure messages.
func truncate(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
