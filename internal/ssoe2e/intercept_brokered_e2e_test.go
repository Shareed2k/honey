//go:build k8s_e2e

// This file proves the server-brokered SSO intercept CONTROL PLANE and the
// RBAC split behind it, against a REAL Keycloak IdP, a REAL honey web server,
// and a REAL k3s cluster. It deliberately does NOT stand up the real
// data-plane interception agent or assert a green/200 deploy: that path is
// already covered by internal/intercept's own e2e (a real ephemeral container,
// real exec, real token delivery) and by the broker's unit tests. Every
// scenario here is a NEGATIVE that the intercept gate rejects before any
// deploy is attempted — see internal/intercept/broker.go Authorize, where
// gate(...) is evaluated first and returns before applyEphemeral ever runs —
// so none of these tests need a running agent or even a target pod that
// exists.
package ssoe2e

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/intercept"
	"github.com/shareed2k/honey/internal/oidc"
	"github.com/shareed2k/honey/internal/policy"
)

// interceptPolicy is one OPA module (package honey) carrying both policies the
// brokered intercept endpoints need, keyed on the SAME realm as
// TestSSOE2E_KubeAndSSH (alice is in group "eng", bob is not):
//
//   - identity, for target "intercept": an eng member maps to an authorized
//     actor (their email); a non-member (bob) resolves no identity, so
//     /intercept/authorize is denied fail-closed before the broker is ever
//     consulted.
//   - the intercept gate itself (action "intercept", evaluated by
//     intercept.Broker.Authorize via internal/intercept/gate.go): an eng
//     member may only intercept in namespace "kube-system". Every scenario
//     below requests namespace "default", so the gate denies — proving both
//     the boundary (a mapped, otherwise-authorized identity can still be
//     refused by the gate) and that the verified id_token claims actually
//     reach the gate (the rule reads input.groups, which gate() populates only
//     from the token's verified groups claim).
const interceptPolicy = `package honey

import rego.v1

default allow := false

identity := {
	"user":   input.email,
	"groups": input.groups,
} if {
	input.action == "identity"
	input.target == "intercept"
	"eng" in input.groups
}

allow if {
	input.action == "identity"
	identity
}

allow if {
	input.action == "intercept"
	"eng" in input.groups
	input.namespace == "kube-system"
}
`

// interceptEnforcer writes interceptPolicy to a temp dir and builds a
// policy.Enforcer from it via the production directory-loading path
// (policy.New) — the same mechanism ssoEnforcer uses, with a different policy
// body tailored to the intercept control-plane actions.
func interceptEnforcer(t *testing.T) *policy.Enforcer {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "intercept.rego"), []byte(interceptPolicy), 0o600))
	enf, err := policy.New(context.Background(), dir, nil)
	require.NoError(t, err)
	return enf
}

// noopExecer is a PodExecer stand-in for the broker's dependency wiring. It is
// never invoked by any scenario in this file: every /authorize negative is
// rejected by the identity policy or the intercept gate before the broker
// reaches an exec, and the RBAC-split scenario never calls the broker at all.
type noopExecer struct{}

// ExecInPod is unreachable in this test; it exists only to satisfy
// intercept.PodExecer.
func (noopExecer) ExecInPod(_ context.Context, _ []string, _ io.Reader, _, _ io.Writer) error {
	return nil
}

// TestSSOE2E_BrokeredIntercept proves, against a real Keycloak IdP, a real
// honey web server, and a real k3s cluster:
//
//  1. The authorization boundary in front of a brokered interception deploy:
//     a bad id_token (401), an identity the policy doesn't map (403), and a
//     mapped identity the intercept gate itself denies (403) — plus two stop
//     negatives (401 unauthenticated, 404 unknown session). None of these
//     reach intercept.Broker.Authorize's deploy step.
//  2. The RBAC split that makes the boundary meaningful even if it were
//     somehow bypassed: an "operator" ServiceAccount scoped to only
//     pods[get] and pods/portforward[create] — a plausible minimal grant for
//     using an already-running interception session — cannot mutate a pod's
//     ephemeral containers (Forbidden), while honey's own credentials (here,
//     the k3s admin identity standing in for honey's service account) can.
//
// Scope boundary: this test does NOT stand up the mogate data-plane agent or
// assert a successful (200) authorize/deploy. That path is covered by
// internal/intercept's own e2e (real ephemeral container, real exec, real
// token delivery) and by the broker's unit tests.
func TestSSOE2E_BrokeredIntercept(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// (1) Real k3s + admin clientset. Honey's own service account is
	// stood in for by this admin identity: the broker's Clientset dep
	// returns it directly, exactly as production returns honey's configured
	// cluster credentials.
	adminRest, adminCS := startK3s(t)

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

	// (3) One enforcer shared by identity resolution AND the intercept gate.
	enf := interceptEnforcer(t)

	// (4) The broker: honey's admin clientset stands in for its service
	// account; the execer is never reached (see noopExecer's doc comment).
	sink := &captureSink{}
	broker := intercept.NewBroker(intercept.BrokerDeps{
		Clientset:  func(string) (kubernetes.Interface, error) { return adminCS, nil },
		Execer:     func(_, _, _, _ string) (intercept.PodExecer, error) { return noopExecer{}, nil },
		Enforcer:   enf,
		Sink:       sink,
		SessionTTL: time.Hour,
	})

	// (5) The REAL production server, with the brokered intercept routes
	// registered (OIDCVerifier + InterceptBroker both set).
	apiAddr, _ := startServer(t, serverArgs{
		enf:             enf,
		verifier:        verifier,
		issuer:          issuer,
		adminRest:       adminRest,
		sink:            sink,
		interceptBroker: broker,
		interceptModes:  []string{"egress"},
	})

	aliceToken := fetchIDToken(t, issuer, "alice", alicePassword) // mapped: in "eng"
	bobToken := fetchIDToken(t, issuer, "bob", bobPassword)       // unmapped: not in "eng"

	authorizeURL := "http://" + apiAddr + "/api/v1/intercept/authorize"
	stopURL := "http://" + apiAddr + "/api/v1/intercept/no-such-session/stop"

	t.Run("bad_token_401", func(t *testing.T) {
		status, body := postJSON(t, authorizeURL, map[string]any{
			"id_token": "garbage",
		})
		require.Equal(t, http.StatusUnauthorized, status, "authorize with a bad id_token: %s", truncate(body))
	})

	t.Run("denied_identity_403", func(t *testing.T) {
		status, body := postJSON(t, authorizeURL, map[string]any{
			"id_token":  bobToken,
			"cluster":   clusterName,
			"namespace": "default",
			"pod":       "whatever",
			"mode":      []string{"egress"},
		})
		require.Equal(t, http.StatusForbidden, status,
			"a subject with no identity mapping must be denied: %s", truncate(body))
	})

	t.Run("denied_by_gate_403_no_deploy", func(t *testing.T) {
		// alice IS mapped, but the gate only allows namespace "kube-system";
		// the target pod need not exist because the gate rejects before any
		// k8s call is made.
		status, body := postJSON(t, authorizeURL, map[string]any{
			"id_token":  aliceToken,
			"cluster":   clusterName,
			"namespace": "default",
			"pod":       "whatever",
			"mode":      []string{"egress"},
		})
		require.Equal(t, http.StatusForbidden, status,
			"the intercept gate must deny namespace \"default\": %s", truncate(body))
	})

	t.Run("stop_unknown_session_404", func(t *testing.T) {
		status, body := postJSON(t, stopURL, map[string]any{
			"id_token": aliceToken,
		})
		require.Equal(t, http.StatusNotFound, status, "stopping an unknown session: %s", truncate(body))
		// Self-contained proof the route is mounted and the HANDLER produced this
		// 404 (its JSON "unknown session" body), not chi's plain-text 404 for an
		// unregistered route ("404 page not found").
		require.Contains(t, body, "unknown session", "expected the handler's JSON 404, got: %s", truncate(body))
	})

	t.Run("stop_bad_token_401", func(t *testing.T) {
		status, body := postJSON(t, stopURL, map[string]any{
			"id_token": "garbage",
		})
		require.Equal(t, http.StatusUnauthorized, status, "stop with a bad id_token: %s", truncate(body))
	})

	t.Run("rbac_split_operator_cannot_deploy", func(t *testing.T) {
		testInterceptRBACSplit(t, adminRest, adminCS)
	})
}

// testInterceptRBACSplit proves the authoritative boundary standing behind the
// control-plane gate: against the SAME real k3s cluster, an "operator"
// ServiceAccount scoped to only pods[get] and pods/portforward[create] cannot
// mutate a pod's ephemeral containers (Forbidden), while honey's own
// credentials (here, the k3s admin identity) can. Even a bypassed
// control-plane gate could not hand an operator the power to deploy an
// interception agent themselves.
func testInterceptRBACSplit(t *testing.T, adminRest *rest.Config, adminCS *kubernetes.Clientset) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	const ns = "rbac-split"
	const podName = "rbac-probe"
	const saName = "operator"

	_, err := adminCS.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = adminCS.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{})
	})

	_, err = adminCS.CoreV1().ServiceAccounts(ns).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: saName, Namespace: ns},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// The narrowest RBAC an operator plausibly needs to USE an
	// already-running interception session: read the pod, port-forward to
	// it. Deliberately NO pods/ephemeralcontainers grant — deploying an
	// agent is honey's job alone.
	_, err = adminCS.RbacV1().Roles(ns).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "operator-role", Namespace: ns},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"pods/portforward"}, Verbs: []string{"create"}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = adminCS.RbacV1().RoleBindings(ns).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "operator-binding", Namespace: ns},
		Subjects:   []rbacv1.Subject{{Kind: "ServiceAccount", Name: saName, Namespace: ns}},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "operator-role"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = adminCS.CoreV1().Pods(ns).Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: ns},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:    "main",
				Image:   "busybox:1.36",
				Command: []string{"sleep", "3600"},
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		p, gerr := adminCS.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
		return gerr == nil && p.Status.Phase == corev1.PodRunning
	}, 90*time.Second, 2*time.Second, "probe pod did not become Running")

	// Mint a real token for the operator ServiceAccount and build a client
	// authenticating as it (same cluster, same CA — only the credential
	// changes).
	expirationSeconds := int64(600)
	tr, err := adminCS.CoreV1().ServiceAccounts(ns).CreateToken(ctx, saName, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expirationSeconds},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, tr.Status.Token, "TokenRequest returned no token")

	operatorRest := *adminRest
	operatorRest.BearerToken = tr.Status.Token
	operatorRest.BearerTokenFile = ""
	operatorRest.Username = ""
	operatorRest.Password = ""
	operatorRest.TLSClientConfig.CertData = nil
	operatorRest.TLSClientConfig.CertFile = ""
	operatorRest.TLSClientConfig.KeyData = nil
	operatorRest.TLSClientConfig.KeyFile = ""
	operatorCS, err := kubernetes.NewForConfig(&operatorRest)
	require.NoError(t, err)

	// The operator's RBAC grant covers Get (proving the grant is live)...
	pod, err := operatorCS.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
	require.NoError(t, err, "operator must be able to get the pod (granted by RBAC)")

	// ...but NOT deploying an ephemeral container: the authoritative boundary.
	pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:    "operator-attempt",
			Image:   "busybox:1.36",
			Command: []string{"sleep", "3600"},
		},
		TargetContainerName: "main",
	})
	_, err = operatorCS.CoreV1().Pods(ns).UpdateEphemeralContainers(ctx, podName, pod, metav1.UpdateOptions{})
	require.Error(t, err, "operator must NOT be able to deploy an ephemeral container")
	require.True(t, apierrors.IsForbidden(err), "expected a Forbidden error, got: %v", err)

	// Honey's own credentials (standing in: the k3s admin identity) CAN — this
	// is what the broker relies on to actually deploy agents in production.
	adminPod, err := adminCS.CoreV1().Pods(ns).Get(ctx, podName, metav1.GetOptions{})
	require.NoError(t, err)
	adminPod.Spec.EphemeralContainers = append(adminPod.Spec.EphemeralContainers, corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:    "honey-attempt",
			Image:   "busybox:1.36",
			Command: []string{"sleep", "3600"},
		},
		TargetContainerName: "main",
	})
	_, err = adminCS.CoreV1().Pods(ns).UpdateEphemeralContainers(ctx, podName, adminPod, metav1.UpdateOptions{})
	// Assert the call SUCCEEDS (not merely "not Forbidden"): a generic failure
	// would mean the call is broken for everyone, which would make the operator's
	// Forbidden above meaningless. NoError subsumes not-Forbidden and proves the
	// authoritative half of the split — honey's service account can deploy.
	require.NoError(t, err, "honey's own credentials must succeed at UpdateEphemeralContainers: %v", err)
}
