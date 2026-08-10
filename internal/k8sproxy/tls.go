package k8sproxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/safepath"
)

const (
	servingCertFile = "k8sproxy_serving.crt"
	servingKeyFile  = "k8sproxy_serving.key"
)

// BuildServerTLSConfig builds the mTLS serving config: the serving keypair
// authenticates the proxy to kubectl, and clientCAPEM is the trust anchor for
// verifying client certificates. Client auth is mandatory
// (RequireAndVerifyClientCert) and TLS 1.2 is the floor.
func BuildServerTLSConfig(servingCertPEM, servingKeyPEM, clientCAPEM []byte) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(servingCertPEM, servingKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("k8sproxy: serving keypair: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(clientCAPEM) {
		return nil, fmt.Errorf("k8sproxy: no client CA certificates parsed from PEM")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// EnsureServingCert loads the proxy's serving keypair from dir, or generates a
// self-signed EC (P-256) serving certificate and persists it there. It is
// idempotent: a second call with the same dir loads the cert written by the
// first. SANs are hosts plus "localhost" and 127.0.0.1; the cert is valid for a
// year.
func EnsureServingCert(dir string, hosts []string) (certPEM, keyPEM []byte, err error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil, fmt.Errorf("k8sproxy: serving cert: empty state dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: serving cert: mkdir: %w", err)
	}
	certPath, err := safepath.JoinUnder(dir, servingCertFile)
	if err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: serving cert path: %w", err)
	}
	keyPath, err := safepath.JoinUnder(dir, servingKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: serving key path: %w", err)
	}

	if cpem, cerr := safepath.ReadFile(certPath); cerr == nil {
		kpem, kerr := safepath.ReadFile(keyPath)
		if kerr != nil {
			return nil, nil, fmt.Errorf("k8sproxy: serving key read: %w", kerr)
		}
		return cpem, kpem, nil
	}

	certPEM, keyPEM, err = generateServingCert(hosts)
	if err != nil {
		return nil, nil, err
	}
	if err := safepath.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: write serving cert: %w", err)
	}
	if err := safepath.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: write serving key: %w", err)
	}
	return certPEM, keyPEM, nil
}

// generateServingCert mints a fresh self-signed EC P-256 serving certificate.
func generateServingCert(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: serving cert: generate key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(),
		Subject:               pkix.Name{CommonName: "honey-k8s-proxy"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	seen := make(map[string]bool)
	addHost := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
			return
		}
		tmpl.DNSNames = append(tmpl.DNSNames, h)
	}
	for _, h := range hosts {
		addHost(h)
	}
	addHost("localhost")
	addHost("127.0.0.1")

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: serving cert: self-sign: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("k8sproxy: serving cert: marshal key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func randSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}
