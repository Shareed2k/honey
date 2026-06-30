package mobile

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

// keysDir returns an app-private directory for transient SSH key material.
// HOME is set by InitDefaultConfig to the app's private files dir on Android.
func keysDir() (string, error) {
	base, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(filepath.Clean(base), ".honey", "keys")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// hasPEMHeader reports whether s carries a PEM private-key header (vs. a file path).
func hasPEMHeader(s string) bool {
	return strings.Contains(s, "-----BEGIN")
}

// materializeKey normalizes an SSH private key for use by the file-path based
// dial path (DialHoneyClient -> goph.Key).
//
//   - If pemOrPath is a plain filesystem path (no PEM header) and no passphrase
//     is given, it is returned unchanged with a no-op cleanup.
//   - Otherwise pemOrPath is treated as PEM bytes: it is decrypted (when a
//     passphrase is supplied), re-marshaled as an UNENCRYPTED PKCS#8 PEM, and
//     written to a 0600 file under the app-private keys dir. cleanup shreds and
//     removes that file.
//
// Security note: the transient unencrypted key lives only in app-private,
// FBE-encrypted storage with 0600 perms and is removed on disconnect. A future
// hardening is to inject an in-memory ssh.Signer into the dial path so the key
// never touches disk.
func materializeKey(pemOrPath, passphrase string) (path string, cleanup func(), err error) {
	noop := func() {}
	pemOrPath = strings.TrimSpace(pemOrPath)
	if pemOrPath == "" {
		return "", noop, nil
	}
	if !hasPEMHeader(pemOrPath) && passphrase == "" {
		// Already a usable file path.
		return pemOrPath, noop, nil
	}

	der, err := decodeToPKCS8([]byte(pemOrPath), passphrase)
	if err != nil {
		return "", noop, err
	}
	unencrypted := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	dir, err := keysDir()
	if err != nil {
		return "", noop, err
	}
	f, err := os.CreateTemp(dir, "active-*.pem")
	if err != nil {
		return "", noop, err
	}
	name := f.Name()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", noop, err
	}
	if _, err := f.Write(unencrypted); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return "", noop, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", noop, err
	}

	cleanup = func() {
		if info, statErr := os.Stat(name); statErr == nil {
			// Best-effort shred: overwrite with zeros before removal.
			if zeros := make([]byte, info.Size()); len(zeros) > 0 {
				_ = os.WriteFile(name, zeros, 0o600)
			}
		}
		_ = os.Remove(name)
	}
	return name, cleanup, nil
}

// decodeToPKCS8 parses a (possibly passphrase-protected) PEM private key and
// returns its DER PKCS#8 encoding.
func decodeToPKCS8(pemBytes []byte, passphrase string) ([]byte, error) {
	var raw any
	var err error
	if passphrase != "" {
		raw, err = ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, []byte(passphrase))
	} else {
		raw, err = ssh.ParseRawPrivateKey(pemBytes)
	}
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	// x509.MarshalPKCS8PrivateKey wants ed25519.PrivateKey by value.
	if k, ok := raw.(*ed25519.PrivateKey); ok {
		raw = *k
	}
	der, err := x509.MarshalPKCS8PrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	return der, nil
}

// KeyFingerprint validates an SSH private key and returns JSON describing it:
// {"type":"ED25519","fingerprint":"SHA256:..."}. A passphrase may be supplied
// for encrypted keys. Returns an error for invalid keys or a wrong passphrase.
func KeyFingerprint(pemKey, passphrase string) (string, error) {
	var signer ssh.Signer
	var err error
	if passphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(pemKey), []byte(passphrase))
	} else {
		signer, err = ssh.ParsePrivateKey([]byte(pemKey))
	}
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	pub := signer.PublicKey()
	out := map[string]string{
		"type":        friendlyKeyType(pub.Type()),
		"fingerprint": ssh.FingerprintSHA256(pub),
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// friendlyKeyType maps an SSH public key algorithm to a short display label.
func friendlyKeyType(t string) string {
	switch {
	case t == "ssh-ed25519" || strings.HasPrefix(t, "ssh-ed25519"):
		return "ED25519"
	case strings.HasPrefix(t, "ecdsa-"):
		return "ECDSA"
	case t == "ssh-rsa" || strings.HasPrefix(t, "rsa-"):
		return "RSA"
	case t == "ssh-dss":
		return "DSA"
	default:
		return strings.ToUpper(t)
	}
}
