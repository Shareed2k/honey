package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// selfSignedForTest mints a throwaway EC P-256 self-signed certificate/key pair
// for use as cache contents in tests. Reused by later kubectl-credential-plugin
// tasks, so keep this signature stable.
func selfSignedForTest(t *testing.T, notAfter time.Time) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-leaf"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyBytes, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	return certPEM, keyPEM
}

func TestCachedCert_StoreLoadRoundTrip(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir()) // isolate the cache root (see kubeCredsDir)
	cert, key := selfSignedForTest(t, time.Now().Add(time.Hour))
	require.NoError(t, storeCachedCert("prod", cachedCert{CertPEM: cert, KeyPEM: key, NotAfter: time.Now().Add(time.Hour)}))

	got, ok := loadCachedCert("prod")
	require.True(t, ok)
	require.Equal(t, cert, got.CertPEM)
	require.Equal(t, key, got.KeyPEM)

	// perms: dir 0700, files 0600
	dir, err := kubeCredsDir("prod")
	require.NoError(t, err)
	di, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), di.Mode().Perm())

	fi, err := os.Stat(filepath.Join(dir, "cert.pem"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	fi, err = os.Stat(filepath.Join(dir, "key.pem"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

func TestCachedCert_LoadMissingReturnsFalse(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())
	_, ok := loadCachedCert("prod")
	require.False(t, ok)
}

func TestCachedCert_LoadCorruptCertReturnsFalse(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())
	_, key := selfSignedForTest(t, time.Now().Add(time.Hour))
	require.NoError(t, storeCachedCert("prod", cachedCert{CertPEM: []byte("not a pem"), KeyPEM: key}))

	_, ok := loadCachedCert("prod")
	require.False(t, ok)
}

func TestCachedCert_LoadMissingKeyReturnsFalse(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())
	dir, err := kubeCredsDir("prod")
	require.NoError(t, err)
	cert, _ := selfSignedForTest(t, time.Now().Add(time.Hour))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cert.pem"), cert, 0o600))
	// no key.pem written

	_, ok := loadCachedCert("prod")
	require.False(t, ok)
}

// TestCachedCert_LoadCorruptKeyReturnsFalse proves loadCachedCert validates
// key.pem (not just cert.pem): a garbage/partial key must never be handed
// back verbatim, even if the cert alongside it is fine.
func TestCachedCert_LoadCorruptKeyReturnsFalse(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())
	cert, key := selfSignedForTest(t, time.Now().Add(time.Hour))
	require.NoError(t, storeCachedCert("prod", cachedCert{CertPEM: cert, KeyPEM: key}))

	dir, err := kubeCredsDir("prod")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "key.pem"), []byte("not a pem"), 0o600))

	_, ok := loadCachedCert("prod")
	require.False(t, ok)
}

// TestCachedCert_PerClusterIsolation proves the cache is keyed per cluster:
// storing under one cluster name must not be visible under another, and each
// cluster's own entry must round-trip independently.
func TestCachedCert_PerClusterIsolation(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())

	prodCert, prodKey := selfSignedForTest(t, time.Now().Add(time.Hour))
	require.NoError(t, storeCachedCert("prod", cachedCert{CertPEM: prodCert, KeyPEM: prodKey}))

	stagingCert, stagingKey := selfSignedForTest(t, time.Now().Add(2*time.Hour))
	require.NoError(t, storeCachedCert("staging", cachedCert{CertPEM: stagingCert, KeyPEM: stagingKey}))

	got, ok := loadCachedCert("prod")
	require.True(t, ok)
	require.Equal(t, prodCert, got.CertPEM)
	require.Equal(t, prodKey, got.KeyPEM)

	got, ok = loadCachedCert("staging")
	require.True(t, ok)
	require.Equal(t, stagingCert, got.CertPEM)
	require.Equal(t, stagingKey, got.KeyPEM)

	// a third, never-cached cluster must miss.
	_, ok = loadCachedCert("qa")
	require.False(t, ok)
}

// TestCachedCert_DistinctNamesNoCollision proves distinct cluster names that
// sanitize to the same prefix (a "/" and a "_" both fold to "_") still land
// in different cache dirs and never read/clobber each other's cert.
func TestCachedCert_DistinctNamesNoCollision(t *testing.T) {
	t.Setenv("HONEY_HOME", t.TempDir())

	slashCert, slashKey := selfSignedForTest(t, time.Now().Add(time.Hour))
	require.NoError(t, storeCachedCert("prod/x", cachedCert{CertPEM: slashCert, KeyPEM: slashKey}))

	underscoreCert, underscoreKey := selfSignedForTest(t, time.Now().Add(2*time.Hour))
	require.NoError(t, storeCachedCert("prod_x", cachedCert{CertPEM: underscoreCert, KeyPEM: underscoreKey}))

	slashDir, err := kubeCredsDir("prod/x")
	require.NoError(t, err)
	underscoreDir, err := kubeCredsDir("prod_x")
	require.NoError(t, err)
	require.NotEqual(t, slashDir, underscoreDir, "distinct cluster names must not share a cache dir")

	got, ok := loadCachedCert("prod/x")
	require.True(t, ok)
	require.Equal(t, slashCert, got.CertPEM)
	require.Equal(t, slashKey, got.KeyPEM)

	got, ok = loadCachedCert("prod_x")
	require.True(t, ok)
	require.Equal(t, underscoreCert, got.CertPEM)
	require.Equal(t, underscoreKey, got.KeyPEM)
}

func TestCachedCert_IsFresh(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	require.True(t, cachedCert{NotAfter: now.Add(10 * time.Minute)}.isFresh(now, time.Minute))
	require.False(t, cachedCert{NotAfter: now.Add(30 * time.Second)}.isFresh(now, time.Minute)) // within skew
	require.False(t, cachedCert{}.isFresh(now, time.Minute))                                    // zero NotAfter

	// exact boundary: NotAfter - skew == now → not fresh (isFresh is a strict "before").
	require.False(t, cachedCert{NotAfter: now.Add(time.Minute)}.isFresh(now, time.Minute))
	// one nanosecond past the boundary → fresh.
	require.True(t, cachedCert{NotAfter: now.Add(time.Minute + time.Nanosecond)}.isFresh(now, time.Minute))

	// skew genuinely changes the renewal threshold: same NotAfter, different
	// skew values land on opposite sides of freshness.
	require.True(t, cachedCert{NotAfter: now.Add(2 * time.Minute)}.isFresh(now, 90*time.Second))
	require.False(t, cachedCert{NotAfter: now.Add(2 * time.Minute)}.isFresh(now, 3*time.Minute))
}

func TestCachedCert_CertNotAfter(t *testing.T) {
	want := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	certPEM, _ := selfSignedForTest(t, want)

	got, err := certNotAfter(certPEM)
	require.NoError(t, err)
	require.True(t, got.Equal(want), "got %v, want %v", got, want)
}

func TestCachedCert_CertNotAfterInvalidPEM(t *testing.T) {
	_, err := certNotAfter([]byte("not a pem"))
	require.Error(t, err)
}

// TestCachedCert_SanitizesClusterName proves a crafted, traversal-y --cluster
// value is reduced to a single safe path segment that stays under the cache
// root, across several traversal styles (relative "../", bare "..", an
// absolute leading "/", and a Windows-style "..\..\").
func TestCachedCert_SanitizesClusterName(t *testing.T) {
	names := []string{
		"../../etc/passwd",
		"..",
		"/etc/passwd",
		"..\\..\\windows",
	}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("HONEY_HOME", base)

			dir, err := kubeCredsDir(name)
			require.NoError(t, err)

			// The resolved dir must live directly under <base>/kube/<segment>, i.e.
			// the path must not have escaped outside base via traversal.
			kubeRoot := filepath.Join(base, "kube")
			rel, err := filepath.Rel(kubeRoot, dir)
			require.NoError(t, err)
			require.False(t, strings.HasPrefix(rel, ".."), "cache dir %q escaped cache root %q", dir, kubeRoot)
			require.False(t, filepath.IsAbs(rel))
			require.Equal(t, filepath.Base(dir), rel, "cluster name must sanitize to a single path segment")

			// The sanitized segment itself must contain no separators or literal
			// dots, which would otherwise allow traversal.
			segment := filepath.Base(dir)
			require.NotContains(t, segment, "/")
			require.NotContains(t, segment, "\\")
			require.NotContains(t, segment, ".")

			// The dir must actually have been created (0700) under the cache root.
			di, err := os.Stat(dir)
			require.NoError(t, err)
			require.True(t, di.IsDir())
			require.Equal(t, os.FileMode(0o700), di.Mode().Perm())
		})
	}
}
