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
