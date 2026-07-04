package webserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/safepath"
)

const (
	deviceCACertFile = "device_ca.crt"
	deviceCAKeyFile  = "device_ca.key"
)

// DeviceCA is a minimal certificate authority that signs short-lived client
// certificates for enrolled devices. The CA keypair (EC P-256) is persisted
// under the state dir and reused across restarts. Its public cert goes into the
// gateway's mTLS client-CA trust store (see examples/mtls/apisix).
type DeviceCA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
}

// LoadOrCreateDeviceCA loads the CA from dir, or generates + persists one.
func LoadOrCreateDeviceCA(dir string) (*DeviceCA, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("device CA: empty state dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("device CA: mkdir: %w", err)
	}
	// Constrain CA file access under dir (safepath => os.Root, no path traversal).
	certPath, err := safepath.JoinUnder(dir, deviceCACertFile)
	if err != nil {
		return nil, fmt.Errorf("device CA: cert path: %w", err)
	}
	keyPath, err := safepath.JoinUnder(dir, deviceCAKeyFile)
	if err != nil {
		return nil, fmt.Errorf("device CA: key path: %w", err)
	}

	if cpem, cerr := safepath.ReadFile(certPath); cerr == nil {
		kpem, kerr := safepath.ReadFile(keyPath)
		if kerr != nil {
			return nil, fmt.Errorf("device CA: read key: %w", kerr)
		}
		return parseDeviceCA(cpem, kpem)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("device CA: generate key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          randSerial(),
		Subject:               pkix.Name{CommonName: "honey-device-ca"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("device CA: self-sign: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("device CA: marshal key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := safepath.WriteFile(certPath, certPEM, 0o600); err != nil {
		return nil, fmt.Errorf("device CA: write cert: %w", err)
	}
	if err := safepath.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("device CA: write key: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &DeviceCA{cert: cert, key: key, certPEM: certPEM}, nil
}

func parseDeviceCA(certPEM, keyPEM []byte) (*DeviceCA, error) {
	cb, _ := pem.Decode(certPEM)
	if cb == nil {
		return nil, fmt.Errorf("device CA: invalid cert PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("device CA: parse cert: %w", err)
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, fmt.Errorf("device CA: invalid key PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, fmt.Errorf("device CA: parse key: %w", err)
	}
	return &DeviceCA{cert: cert, key: key, certPEM: certPEM}, nil
}

// Sign issues a client certificate for cn (valid for ttl) using the CSR's public
// key. The CSR signature is verified first.
func (ca *DeviceCA) Sign(csr *x509.CertificateRequest, cn string, ttl time.Duration) ([]byte, error) {
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("csr signature: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: randSerial(),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(ttl),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, csr.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("sign client cert: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// CertPEM returns the CA certificate in PEM form (for the gateway trust store).
func (ca *DeviceCA) CertPEM() []byte { return ca.certPEM }

// Fingerprint is the hex SHA-256 of the CA certificate DER (for pinning).
func (ca *DeviceCA) Fingerprint() string {
	sum := sha256.Sum256(ca.cert.Raw)
	return hex.EncodeToString(sum[:])
}

func randSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}
