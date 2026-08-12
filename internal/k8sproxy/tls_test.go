package k8sproxy

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildServerTLSConfig_Good(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := generateServingCert([]string{"honey.local"})
	require.NoError(t, err)

	cfg, err := BuildServerTLSConfig(certPEM, keyPEM, ca.certPEM)
	require.NoError(t, err)
	require.Equal(t, tls.RequireAndVerifyClientCert, cfg.ClientAuth)
	require.EqualValues(t, tls.VersionTLS13, cfg.MinVersion)
	require.NotNil(t, cfg.ClientCAs)
	require.Len(t, cfg.Certificates, 1)
}

func TestBuildServerTLSConfig_BadServingPEM(t *testing.T) {
	ca := newTestCA(t)
	_, err := BuildServerTLSConfig([]byte("not a cert"), []byte("not a key"), ca.certPEM)
	require.Error(t, err)
}

func TestBuildServerTLSConfig_EmptyClientCA(t *testing.T) {
	certPEM, keyPEM, err := generateServingCert(nil)
	require.NoError(t, err)

	_, err = BuildServerTLSConfig(certPEM, keyPEM, []byte("no certs here"))
	require.Error(t, err)
}

func TestEnsureServingCert_Idempotent(t *testing.T) {
	dir := t.TempDir()

	certPEM1, keyPEM1, err := EnsureServingCert(dir, []string{"honey.local"})
	require.NoError(t, err)
	require.NotEmpty(t, certPEM1)
	require.NotEmpty(t, keyPEM1)

	// A second call loads the persisted cert rather than regenerating.
	certPEM2, keyPEM2, err := EnsureServingCert(dir, []string{"honey.local"})
	require.NoError(t, err)
	require.Equal(t, certPEM1, certPEM2)
	require.Equal(t, keyPEM1, keyPEM2)

	// The generated keypair is a valid serving certificate.
	_, err = tls.X509KeyPair(certPEM1, keyPEM1)
	require.NoError(t, err)
}

func TestEnsureServingCert_EmptyDir(t *testing.T) {
	_, _, err := EnsureServingCert("", nil)
	require.Error(t, err)
}

func TestResolveClientCA_FallbackAndMissing(t *testing.T) {
	// No path, no default => error (fail closed).
	_, err := resolveClientCA(ServerConfig{}, nil)
	require.Error(t, err)

	// No path, default present => uses default.
	pem, err := resolveClientCA(ServerConfig{}, []byte("default-ca"))
	require.NoError(t, err)
	require.Equal(t, []byte("default-ca"), pem)
}

func TestServingCAPath(t *testing.T) {
	dir := t.TempDir()
	p := ServingCAPath(dir)
	require.Contains(t, p, servingCertFile)
}
