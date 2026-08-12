package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	clientauthenticationv1 "k8s.io/client-go/pkg/apis/clientauthentication/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

// newKubeCredCmd returns a bare *cobra.Command suitable for exercising
// runKubeCredential directly: it has a background context (fetchKubeCertViaSSO
// takes cmd.Context()) and its stdout captured to the returned buffer so
// tests can assert exactly what would have been written to the real stdout.
func newKubeCredCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)
	return cmd, &out
}

func TestEmitExecCredential_StdoutOnlyJSON(t *testing.T) {
	cert, key := selfSignedForTest(t, time.Now().Add(time.Hour))
	var out bytes.Buffer
	require.NoError(t, emitExecCredential(&out, cachedCert{CertPEM: cert, KeyPEM: key, NotAfter: time.Now().Add(time.Hour)}))

	var ec clientauthenticationv1.ExecCredential
	require.NoError(t, json.Unmarshal(out.Bytes(), &ec))
	require.Equal(t, "client.authentication.k8s.io/v1", ec.APIVersion)
	require.Equal(t, "ExecCredential", ec.Kind)
	require.NotNil(t, ec.Status)
	require.Equal(t, string(cert), ec.Status.ClientCertificateData)
	require.Equal(t, string(key), ec.Status.ClientKeyData)
	require.NotNil(t, ec.Status.ExpirationTimestamp)
}

func TestRunKubeCredential_FreshCacheEmitsWithoutNetwork(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())
	cert, key := selfSignedForTest(t, time.Now().Add(time.Hour))
	require.NoError(t, storeCachedCert("prod", cachedCert{CertPEM: cert, KeyPEM: key, NotAfter: time.Now().Add(time.Hour)}))

	// Fail loudly if the network path is taken.
	origFlow := browserAuthCodeFlowFn
	t.Cleanup(func() { browserAuthCodeFlowFn = origFlow })
	browserAuthCodeFlowFn = func(context.Context, string, []string) (string, string, error) {
		t.Fatal("must not re-auth on a fresh cache")
		return "", "", nil
	}

	cmd, out := newKubeCredCmd(t)
	require.NoError(t, runKubeCredential(cmd, "prod", "http://admin"))

	var ec clientauthenticationv1.ExecCredential
	require.NoError(t, json.Unmarshal(out.Bytes(), &ec))
	require.NotNil(t, ec.Status)
	require.Equal(t, string(cert), ec.Status.ClientCertificateData)
	require.Equal(t, string(key), ec.Status.ClientKeyData)
}

func TestRunKubeCredential_StaleNonInteractiveErrors(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())
	t.Setenv("KUBERNETES_EXEC_INFO", `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"interactive":false}}`)
	cert, key := selfSignedForTest(t, time.Now().Add(-time.Minute)) // already expired
	require.NoError(t, storeCachedCert("prod", cachedCert{CertPEM: cert, KeyPEM: key, NotAfter: time.Now().Add(-time.Minute)}))

	origFlow := browserAuthCodeFlowFn
	t.Cleanup(func() { browserAuthCodeFlowFn = origFlow })
	browserAuthCodeFlowFn = func(context.Context, string, []string) (string, string, error) {
		t.Fatal("must not re-auth when kubectl disallows interactive use")
		return "", "", nil
	}

	cmd, out := newKubeCredCmd(t)
	err := runKubeCredential(cmd, "prod", "http://admin")
	require.Error(t, err)
	require.Contains(t, err.Error(), "interactive")
	require.Empty(t, out.Bytes(), "no partial credential should be written to stdout on error")
}

func TestExecInteractiveAllowed(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		t.Setenv("KUBERNETES_EXEC_INFO", "")
		require.False(t, execInteractiveAllowed())
	})
	t.Run("explicit false", func(t *testing.T) {
		t.Setenv("KUBERNETES_EXEC_INFO", `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"interactive":false}}`)
		require.False(t, execInteractiveAllowed())
	})
	t.Run("explicit true", func(t *testing.T) {
		t.Setenv("KUBERNETES_EXEC_INFO", `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"interactive":true}}`)
		require.True(t, execInteractiveAllowed())
	})
	t.Run("malformed json is conservative", func(t *testing.T) {
		t.Setenv("KUBERNETES_EXEC_INFO", `not json`)
		require.False(t, execInteractiveAllowed())
	})
}

func TestInExecCredentialMode(t *testing.T) {
	t.Setenv("KUBERNETES_EXEC_INFO", "")
	require.False(t, inExecCredentialMode())

	t.Setenv("KUBERNETES_EXEC_INFO", `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential"}`)
	require.True(t, inExecCredentialMode())
}

func TestRunKubeCredential_MissingCacheInteractiveFetchesAndCaches(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())
	t.Setenv("KUBERNETES_EXEC_INFO", `{"apiVersion":"client.authentication.k8s.io/v1","kind":"ExecCredential","spec":{"interactive":true}}`)

	cert, _ := selfSignedForTest(t, time.Now().Add(time.Hour))

	origFlow := browserAuthCodeFlowFn
	origLogin := kubeOIDCLoginFn
	t.Cleanup(func() {
		browserAuthCodeFlowFn = origFlow
		kubeOIDCLoginFn = origLogin
	})
	browserAuthCodeFlowFn = func(context.Context, string, []string) (string, string, error) {
		return "id-token", "nonce", nil
	}
	kubeOIDCLoginFn = func(_ context.Context, _, _, _, _ string, _ []byte) (certPEM, caPEM []byte, cn string, groups []string, err error) {
		return cert, []byte("ca-pem"), "alice", nil, nil
	}

	cmd, out := newKubeCredCmd(t)
	require.NoError(t, runKubeCredential(cmd, "prod", "http://admin"))

	var ec clientauthenticationv1.ExecCredential
	require.NoError(t, json.Unmarshal(out.Bytes(), &ec))
	require.NotNil(t, ec.Status)
	require.Equal(t, string(cert), ec.Status.ClientCertificateData)

	// The freshly fetched cert must now be cached for next time. loadCachedCert
	// also validates that the key on disk matches the cert on disk, so read the
	// raw file instead: the stubbed kubeOIDCLoginFn returns a self-signed test
	// cert that intentionally does not pair with fetchKubeCertViaSSO's own
	// internally generated key (there is no seam to control that key), so a
	// full key-matched round trip isn't meaningful here.
	dir, err := kubeCredsDir("prod")
	require.NoError(t, err)
	gotCert, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	require.NoError(t, err)
	require.Equal(t, cert, gotCert)
}

func TestWriteExecKubeContext_UsesExecAuthInfo(t *testing.T) {
	cfg := writeExecKubeContext(api.NewConfig(), kubeContextOpts{
		cluster: "prod", proxy: "proxy:6443", cn: "alice@corp", caPEM: []byte("ca"), contextName: "honey-prod",
	}, "https://admin.example")

	ai := cfg.AuthInfos["honey-alice@corp"]
	require.NotNil(t, ai)
	require.NotNil(t, ai.Exec)
	require.Equal(t, "honey", ai.Exec.Command)
	require.Equal(t, "client.authentication.k8s.io/v1", ai.Exec.APIVersion)
	// cluster is a POSITIONAL argument on kubeLoginCmd (Args: cobra.ExactArgs(1)),
	// not a flag: there is no --cluster flag. See
	// TestWriteExecKubeContext_ArgsParseThroughRealCommand for a test that
	// catches this class of drift by actually running the Args through cobra.
	require.Equal(t, []string{"kube", "login", "prod", "--admin-url", "https://admin.example"}, ai.Exec.Args)
	require.Equal(t, api.IfAvailableExecInteractiveMode, ai.Exec.InteractiveMode)
	require.False(t, ai.Exec.ProvideClusterInfo)
	require.Empty(t, ai.ClientCertificateData) // NOT static
	require.Empty(t, ai.ClientKeyData)

	require.Equal(t, "https://proxy:6443/prod", cfg.Clusters["honey-prod"].Server)
	require.Equal(t, []byte("ca"), cfg.Clusters["honey-prod"].CertificateAuthorityData)

	ctx := cfg.Contexts["honey-prod"]
	require.NotNil(t, ctx)
	require.Equal(t, "honey-prod", ctx.Cluster)
	require.Equal(t, "honey-alice@corp", ctx.AuthInfo)
	require.Equal(t, "honey-prod", cfg.CurrentContext)
}

// TestWriteExecKubeContext_ArgsParseThroughRealCommand feeds
// writeExecKubeContext's emitted Exec.Args into the real "honey" command tree
// (rootCmd -> kube -> login), exactly as kubectl invokes them ("honey" plus
// Args verbatim, cluster and admin-url only). It exists to catch drift
// between writeExecKubeContext's hardcoded Args and kubeLoginCmd's actual
// flag/positional-argument contract: a struct-only assertion on api.AuthInfo
// (as in TestWriteExecKubeContext_UsesExecAuthInfo) cannot detect that,
// because it never asks cobra to parse the Args at all. This test previously
// caught a real regression: Args used to include a "--cluster" flag that
// kubeLoginCmd never defined (cluster is positional), which cobra rejected
// with "unknown flag: --cluster" — i.e. every kubectl API call under the
// default (non-static) exec-plugin kubeconfig would have failed closed.
func TestWriteExecKubeContext_ArgsParseThroughRealCommand(t *testing.T) {
	// Sandbox rootCmd's PersistentPreRunE (logger/config/mesh bootstrap) away
	// from this machine's real environment: no debug log file, no resolvable
	// config file, so it takes its early-return no-op paths.
	t.Setenv("HONEY_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", t.TempDir())
	resetKubeLoginFlags(t)

	cfg := writeExecKubeContext(api.NewConfig(), kubeContextOpts{
		cluster: "prod", proxy: "proxy:6443", cn: "alice", caPEM: []byte("ca"), contextName: "honey-prod",
	}, "https://admin.example")
	ai := cfg.AuthInfos["honey-alice"]
	require.NotNil(t, ai)
	require.NotNil(t, ai.Exec)

	var gotArgs []string
	var gotAdminURL string
	origRunE := kubeLoginCmd.RunE
	t.Cleanup(func() { kubeLoginCmd.RunE = origRunE })
	kubeLoginCmd.RunE = func(_ *cobra.Command, args []string) error {
		gotArgs = args
		gotAdminURL = kubeLoginAdminURL
		return nil
	}

	var out, errOut bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errOut)
	t.Cleanup(func() {
		rootCmd.SetOut(os.Stdout)
		rootCmd.SetErr(os.Stderr)
	})

	// ai.Exec.Command is "honey"; ai.Exec.Args is everything after it, so it
	// is exactly what rootCmd.SetArgs expects.
	rootCmd.SetArgs(ai.Exec.Args)
	err := rootCmd.Execute()
	require.NoError(t, err, "exec Args must parse cleanly through the real command tree (no unknown-flag error): %s", errOut.String())

	require.Equal(t, []string{"prod"}, gotArgs, "cluster must arrive as the positional arg")
	require.Equal(t, "https://admin.example", gotAdminURL, "--admin-url must be parsed by kubeLoginCmd's real flag")
}

func TestWriteExecKubeContext_InsecureSkipTLSVerify(t *testing.T) {
	cfg := writeExecKubeContext(nil, kubeContextOpts{
		cluster: "prod", proxy: "proxy:6443", cn: "alice",
		insecureSkipTLSVerify: true, contextName: "honey-prod",
	}, "https://admin.example")

	cluster := cfg.Clusters["honey-prod"]
	require.True(t, cluster.InsecureSkipTLSVerify)
	require.Empty(t, cluster.CertificateAuthorityData)
}

func TestWriteExecKubeContext_PreservesUnrelatedEntries(t *testing.T) {
	existing := api.NewConfig()
	existing.Clusters["other-cluster"] = &api.Cluster{Server: "https://other.example"}
	existing.AuthInfos["other-user"] = &api.AuthInfo{Token: "tok"}
	existing.Contexts["other-context"] = &api.Context{Cluster: "other-cluster", AuthInfo: "other-user"}
	existing.CurrentContext = "other-context"

	got := writeExecKubeContext(existing, kubeContextOpts{
		cluster: "prod", proxy: "proxy:6443", cn: "alice", caPEM: []byte("ca"), contextName: "honey-prod",
	}, "https://admin.example")

	require.Contains(t, got.Clusters, "other-cluster")
	require.Contains(t, got.AuthInfos, "other-user")
	require.Contains(t, got.Contexts, "other-context")
	require.Contains(t, got.Clusters, "honey-prod")
	require.Equal(t, "honey-prod", got.CurrentContext)
}

// resetKubeLoginFlags snapshots the kube login package-level flag vars and
// registers a cleanup that restores them, so tests driving runKubeLogin
// directly (bypassing cobra flag parsing) don't leak state into other tests.
func resetKubeLoginFlags(t *testing.T) {
	t.Helper()
	orig := struct {
		enrollCode, proxy, adminURL, proxyCA, kubeconfig, context string
		insecure, static                                          bool
	}{
		kubeLoginEnrollCode, kubeLoginProxy, kubeLoginAdminURL, kubeLoginProxyCA,
		kubeLoginKubeconfig, kubeLoginContext, kubeLoginInsecure, kubeLoginStatic,
	}
	t.Cleanup(func() {
		kubeLoginEnrollCode, kubeLoginProxy, kubeLoginAdminURL, kubeLoginProxyCA = orig.enrollCode, orig.proxy, orig.adminURL, orig.proxyCA
		kubeLoginKubeconfig, kubeLoginContext, kubeLoginInsecure, kubeLoginStatic = orig.kubeconfig, orig.context, orig.insecure, orig.static
	})
	kubeLoginEnrollCode, kubeLoginProxyCA, kubeLoginContext = "", "", ""
	kubeLoginInsecure = false
}

// stubSSOLogin makes browserAuthCodeFlowFn and kubeOIDCLoginFn return a fixed
// cert/CA/cn for the duration of the test, so runKubeLogin's SSO branch
// exercises real kubeconfig-writing and caching logic without a network
// round trip.
func stubSSOLogin(t *testing.T, certPEM, caPEM []byte, cn string) {
	t.Helper()
	origFlow, origLogin := browserAuthCodeFlowFn, kubeOIDCLoginFn
	t.Cleanup(func() { browserAuthCodeFlowFn, kubeOIDCLoginFn = origFlow, origLogin })
	browserAuthCodeFlowFn = func(context.Context, string, []string) (string, string, error) {
		return "id-token", "nonce", nil
	}
	kubeOIDCLoginFn = func(_ context.Context, _, _, _, _ string, _ []byte) (cert, ca []byte, gotCN string, groups []string, err error) {
		return certPEM, caPEM, cn, nil, nil
	}
}

func TestRunKubeLogin_SSODefaultWritesExecPluginAndCaches(t *testing.T) {
	t.Setenv("KUBERNETES_EXEC_INFO", "")
	t.Setenv("HONEY_HOME", t.TempDir())
	resetKubeLoginFlags(t)

	cert, _ := selfSignedForTest(t, time.Now().Add(time.Hour))
	stubSSOLogin(t, cert, []byte("ca-pem"), "alice")

	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	kubeLoginProxy = "proxy.example:6443"
	kubeLoginAdminURL = "http://admin.example"
	kubeLoginKubeconfig = kubeconfigPath
	kubeLoginStatic = false

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	require.NoError(t, runKubeLogin(cmd, []string{"prod"}))

	cfg, err := clientcmd.LoadFromFile(kubeconfigPath)
	require.NoError(t, err)
	ai := cfg.AuthInfos["honey-alice"]
	require.NotNil(t, ai)
	require.NotNil(t, ai.Exec)
	require.Equal(t, "honey", ai.Exec.Command)
	require.Equal(t, []string{"kube", "login", "prod", "--admin-url", "http://admin.example"}, ai.Exec.Args)
	require.Empty(t, ai.ClientCertificateData)
	require.Empty(t, ai.ClientKeyData)

	// The fetched certificate must be cached so the exec plugin can serve it
	// without a network round trip next time kubectl invokes honey.
	// loadCachedCert also validates that the key on disk matches the cert on
	// disk, so read the raw file instead: the stubbed kubeOIDCLoginFn returns a
	// self-signed test cert that intentionally does not pair with
	// fetchKubeCertViaSSO's own internally generated key (there is no seam to
	// control that key), so a full key-matched round trip isn't meaningful here
	// (see TestRunKubeCredential_MissingCacheInteractiveFetchesAndCaches).
	dir, err := kubeCredsDir("prod")
	require.NoError(t, err)
	gotCert, err := os.ReadFile(filepath.Join(dir, "cert.pem"))
	require.NoError(t, err)
	require.Equal(t, cert, gotCert)

	require.Contains(t, out.String(), "refresh")
}

func TestRunKubeLogin_SSOStaticWritesEmbeddedCert(t *testing.T) {
	t.Setenv("KUBERNETES_EXEC_INFO", "")
	t.Setenv("HONEY_HOME", t.TempDir())
	resetKubeLoginFlags(t)

	cert, _ := selfSignedForTest(t, time.Now().Add(time.Hour))
	stubSSOLogin(t, cert, []byte("ca-pem"), "alice")

	kubeconfigPath := filepath.Join(t.TempDir(), "config")
	kubeLoginProxy = "proxy.example:6443"
	kubeLoginAdminURL = "http://admin.example"
	kubeLoginKubeconfig = kubeconfigPath
	kubeLoginStatic = true

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)

	require.NoError(t, runKubeLogin(cmd, []string{"prod"}))

	cfg, err := clientcmd.LoadFromFile(kubeconfigPath)
	require.NoError(t, err)
	ai := cfg.AuthInfos["honey-alice"]
	require.NotNil(t, ai)
	require.Nil(t, ai.Exec)
	require.Equal(t, cert, ai.ClientCertificateData)
	require.NotEmpty(t, ai.ClientKeyData)

	require.NotContains(t, out.String(), "refresh")
}
