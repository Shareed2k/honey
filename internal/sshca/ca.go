// Package sshca implements a minimal SSH certificate authority: it persists an
// ed25519 CA keypair under the honey state dir and mints short-lived OpenSSH
// user certificates so operators/CI can authenticate to the SSH gateway without
// distributing per-user keys to every target. The CA public key is what the
// gateway trusts (ssh_gateway.trusted_ca / --trusted-ca).
package sshca

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/safepath"
)

// caKeyFile is the on-disk name of the persisted CA private key (OpenSSH PEM).
const caKeyFile = "ssh_ca_key"

// CA is an SSH certificate authority. It holds the signer derived from the
// persisted CA private key; the CA public key is obtained via signer.PublicKey().
type CA struct {
	signer ssh.Signer
}

// LoadOrCreateCA loads the CA private key from dir, or generates a new ed25519
// key and persists it (OpenSSH PEM). The key path is constrained under dir via
// safepath (os.Root; no traversal), mirroring the gateway host-key pattern.
func LoadOrCreateCA(dir string) (*CA, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("ssh ca: empty state dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ssh ca: mkdir: %w", err)
	}
	keyPath, err := safepath.JoinUnder(dir, caKeyFile)
	if err != nil {
		return nil, fmt.Errorf("ssh ca: path: %w", err)
	}

	if pemBytes, rerr := safepath.ReadFile(keyPath); rerr == nil {
		signer, perr := ssh.ParsePrivateKey(pemBytes)
		if perr != nil {
			return nil, fmt.Errorf("ssh ca: parse: %w", perr)
		}
		return &CA{signer: signer}, nil
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ssh ca: generate: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "honey-ssh-ca")
	if err != nil {
		return nil, fmt.Errorf("ssh ca: marshal: %w", err)
	}
	if err := safepath.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("ssh ca: write: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("ssh ca: signer: %w", err)
	}
	return &CA{signer: signer}, nil
}

// LoadCAPublicKey reads the CA public key from dir without creating anything. If
// the CA key exists it returns (pub, true, nil); if it is absent it returns
// (nil, false, nil); parse or other I/O errors are returned. The gateway uses
// this to auto-trust the built-in CA without minting one.
func LoadCAPublicKey(dir string) (ssh.PublicKey, bool, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, false, fmt.Errorf("ssh ca: empty state dir")
	}
	keyPath, err := safepath.JoinUnder(dir, caKeyFile)
	if err != nil {
		return nil, false, fmt.Errorf("ssh ca: path: %w", err)
	}
	pemBytes, err := safepath.ReadFile(keyPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("ssh ca: read: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, false, fmt.Errorf("ssh ca: parse: %w", err)
	}
	return signer.PublicKey(), true, nil
}

// PublicKey returns the CA's SSH public key (the authority the gateway trusts).
func (c *CA) PublicKey() ssh.PublicKey { return c.signer.PublicKey() }

// AuthorizedKey returns the CA public key as an authorized_keys line (trailing
// newline, as ssh.MarshalAuthorizedKey returns).
func (c *CA) AuthorizedKey() []byte { return ssh.MarshalAuthorizedKey(c.PublicKey()) }

// SignRequest describes a user certificate to mint.
type SignRequest struct {
	// PublicKey is the user's SSH public key to certify (required).
	PublicKey ssh.PublicKey
	// KeyID is a human-readable certificate identifier recorded in the cert.
	KeyID string
	// Principals are the certificate's valid principals (at least one required;
	// a user cert with no principals would be valid for any principal).
	Principals []string
	// TTL is how long the certificate is valid (must be positive).
	TTL time.Duration
}

// Sign mints and signs a short-lived OpenSSH user certificate for req.
func (c *CA) Sign(req SignRequest) (*ssh.Certificate, error) {
	if req.PublicKey == nil {
		return nil, fmt.Errorf("sign: public key is required")
	}
	if len(req.Principals) == 0 {
		return nil, fmt.Errorf("sign: at least one principal is required")
	}
	if req.TTL <= 0 {
		return nil, fmt.Errorf("sign: ttl must be positive")
	}

	var serialBytes [8]byte
	if _, err := rand.Read(serialBytes[:]); err != nil {
		return nil, fmt.Errorf("sign: read serial: %w", err)
	}

	now := time.Now()
	cert := &ssh.Certificate{
		Key:             req.PublicKey,
		Serial:          binary.BigEndian.Uint64(serialBytes[:]),
		CertType:        ssh.UserCert,
		KeyId:           req.KeyID,
		ValidPrincipals: req.Principals,
		ValidAfter:      unixSeconds(now.Add(-1 * time.Minute)),
		ValidBefore:     unixSeconds(now.Add(req.TTL)),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{
				"permit-pty":             "",
				"permit-port-forwarding": "",
			},
		},
	}
	if err := cert.SignCert(rand.Reader, c.signer); err != nil {
		return nil, fmt.Errorf("sign cert: %w", err)
	}
	return cert, nil
}

// unixSeconds returns t's Unix time (seconds) as the uint64 an SSH certificate
// validity field expects. The explicit non-negative guard makes the int64 ->
// uint64 conversion provably safe (a pre-epoch wall-clock time never occurs
// here, but the guard keeps the conversion bounded independent of that).
func unixSeconds(t time.Time) uint64 {
	sec := t.Unix()
	if sec < 0 {
		return 0
	}
	return uint64(sec)
}
