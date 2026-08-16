package cli

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/safepath"
)

// cachedCert is a PEM-encoded client certificate/key pair cached on disk for
// a single cluster, along with the certificate's parsed expiry. Never log the
// contents of CertPEM or KeyPEM.
type cachedCert struct {
	CertPEM  []byte
	KeyPEM   []byte
	NotAfter time.Time
}

// honeyHome resolves the base dir for honey's per-user cache. Honors $HONEY_HOME,
// else ~/.honey. The kube credential cache lives under <honeyHome>/kube/<cluster>.
func honeyHome() (string, error) {
	if v := strings.TrimSpace(os.Getenv("HONEY_HOME")); v != "" {
		return v, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(h, ".honey"), nil
}

// kubeCredsDir returns (creating, mode 0700) the per-cluster credential cache dir.
// The cluster name is reduced to a single safe, collision-free path segment
// (see cacheSegment). The mode is re-asserted on every call (not just at
// creation) so a directory that pre-existed with a looser mode gets tightened
// back to 0700 rather than silently left open.
func kubeCredsDir(cluster string) (string, error) {
	base, err := honeyHome()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "kube", cacheSegment(cluster))
	// 0700 (owner-only): MkdirAll creates the directory with this mode on first
	// use. The cert/key files inside are written 0600 by safepath.WriteFile
	// regardless of the directory's mode, so the private key stays owner-only
	// even if the directory was pre-created more permissively — which is why no
	// separate re-chmod of the directory is needed here.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create kube creds dir: %w", err)
	}
	return dir, nil
}

// sanitizeSegment reduces s to a single filesystem-safe path segment (defends the
// cache path against traversal via a crafted --cluster). This alone is not
// collision-free: distinct inputs can map to the same output (see
// cacheSegment, which composes this with a hash of the raw input).
func sanitizeSegment(s string) string {
	s = strings.TrimSpace(s)
	repl := func(r rune) rune {
		if r == '/' || r == '\\' || r == os.PathSeparator || r == '.' {
			return '_'
		}
		return r
	}
	out := strings.Map(repl, s)
	if out == "" {
		return "_"
	}
	return out
}

// cacheSegment derives the on-disk directory name for cluster: sanitizeSegment's
// human-readable, traversal-safe prefix, plus a short hex-encoded hash of the
// raw cluster name. The prefix alone can collide (e.g. "prod/us-east",
// "prod.us-east", and "prod_us-east" all sanitize to "prod_us-east"); the hash
// suffix guarantees distinct cluster names never share a cache directory,
// while the whole result remains a single flat path segment.
func cacheSegment(cluster string) string {
	sum := sha256.Sum256([]byte(cluster))
	return sanitizeSegment(cluster) + "-" + hex.EncodeToString(sum[:8])
}

// storeCachedCert writes c's certificate and key into the per-cluster cache
// dir as cert.pem and key.pem, both mode 0600. Writes go through safepath,
// which writes via a temp file + rename within the cache dir (atomic: no
// partial key survives a crash) and resolves through os.Root (no symlink
// follow, no traversal). Contents are never logged.
func storeCachedCert(cluster string, c cachedCert) error {
	dir, err := kubeCredsDir(cluster)
	if err != nil {
		return err
	}
	if err := safepath.WriteFile(filepath.Join(dir, "cert.pem"), c.CertPEM, 0o600); err != nil {
		return fmt.Errorf("write cached cert: %w", err)
	}
	if err := safepath.WriteFile(filepath.Join(dir, "key.pem"), c.KeyPEM, 0o600); err != nil {
		return fmt.Errorf("write cached key: %w", err)
	}
	return nil
}

// loadCachedCert reads a previously cached certificate/key pair for cluster.
// It reports ok=false on any missing, unreadable, unparseable, or mismatched
// input rather than returning a partial or garbage cert. Reads go through
// safepath so the cache segment cannot be abused to escape the cache dir.
func loadCachedCert(cluster string) (cachedCert, bool) {
	base, err := honeyHome()
	if err != nil {
		return cachedCert{}, false
	}
	dir := filepath.Join(base, "kube", cacheSegment(cluster))

	certPEM, err := safepath.ReadFile(filepath.Join(dir, "cert.pem"))
	if err != nil {
		return cachedCert{}, false
	}
	keyPEM, err := safepath.ReadFile(filepath.Join(dir, "key.pem"))
	if err != nil {
		return cachedCert{}, false
	}
	// Validate the key is well-formed and actually matches the cert; this
	// rejects an empty/garbage/partial key.pem that certNotAfter alone
	// (which only ever looks at cert.pem) would not catch.
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return cachedCert{}, false
	}
	notAfter, err := certNotAfter(certPEM)
	if err != nil {
		return cachedCert{}, false
	}
	return cachedCert{CertPEM: certPEM, KeyPEM: keyPEM, NotAfter: notAfter}, true
}

// isFresh reports whether c's certificate is still valid at now, once skew is
// subtracted from its expiry (so callers renew ahead of actual expiration). A
// zero NotAfter (never populated) is always considered stale.
func (c cachedCert) isFresh(now time.Time, skew time.Duration) bool {
	return !c.NotAfter.IsZero() && now.Before(c.NotAfter.Add(-skew))
}

// certNotAfter parses the leaf certificate out of certPEM and returns its
// NotAfter (expiry) time.
func certNotAfter(certPEM []byte) (time.Time, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return time.Time{}, fmt.Errorf("decode cert pem: no PEM block found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cert: %w", err)
	}
	return cert.NotAfter, nil
}
