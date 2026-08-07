// Package sshgateway implements an inbound SSH gateway: an ssh.ServerConfig
// front-end that authenticates native ssh clients by an SSH certificate signed
// by a configured trusted CA, maps the certificate to a honey actor, resolves
// the requested resource to an inventory host, and proxies the session to that
// target — recorded, policy-gated, and audited. It drives honey's existing
// downstream modules (ui SSH streamer, engine recorder, cmdgate policy, audit)
// rather than reimplementing them.
package sshgateway

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/safepath"
)

// hostKeyFile is the on-disk name of the persisted gateway host key.
const hostKeyFile = "ssh_gateway_host_key"

// LoadOrCreateHostKey loads the gateway's persistent SSH host key from dir, or
// generates a new ed25519 key and persists it (OpenSSH PEM). The key path is
// constrained under dir via safepath (os.Root; no traversal), mirroring the
// device CA persistence pattern.
func LoadOrCreateHostKey(dir string) (ssh.Signer, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("ssh gateway host key: empty state dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("ssh gateway host key: mkdir: %w", err)
	}
	keyPath, err := safepath.JoinUnder(dir, hostKeyFile)
	if err != nil {
		return nil, fmt.Errorf("ssh gateway host key: path: %w", err)
	}

	if pemBytes, rerr := safepath.ReadFile(keyPath); rerr == nil {
		signer, perr := ssh.ParsePrivateKey(pemBytes)
		if perr != nil {
			return nil, fmt.Errorf("ssh gateway host key: parse: %w", perr)
		}
		return signer, nil
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ssh gateway host key: generate: %w", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "honey-ssh-gateway")
	if err != nil {
		return nil, fmt.Errorf("ssh gateway host key: marshal: %w", err)
	}
	if err := safepath.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("ssh gateway host key: write: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("ssh gateway host key: signer: %w", err)
	}
	return signer, nil
}
