package mobile

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// genEd25519PEM returns an OpenSSH-format private key PEM, optionally encrypted
// with passphrase (empty = unencrypted).
func genEd25519PEM(t *testing.T, passphrase string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	var block *pem.Block
	if passphrase != "" {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "test", []byte(passphrase))
	} else {
		block, err = ssh.MarshalPrivateKey(priv, "test")
	}
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pem.EncodeToMemory(block)
}

func TestKeyFingerprint_ed25519(t *testing.T) {
	pemKey := genEd25519PEM(t, "")
	out, err := KeyFingerprint(string(pemKey), "")
	if err != nil {
		t.Fatalf("KeyFingerprint: %v", err)
	}
	if !strings.Contains(out, `"type":"ED25519"`) {
		t.Errorf("expected ED25519 type, got %s", out)
	}
	if !strings.Contains(out, "SHA256:") {
		t.Errorf("expected SHA256 fingerprint, got %s", out)
	}
}

func TestKeyFingerprint_withPassphrase(t *testing.T) {
	pemKey := genEd25519PEM(t, "s3cret")

	if _, err := KeyFingerprint(string(pemKey), "s3cret"); err != nil {
		t.Fatalf("KeyFingerprint with correct passphrase: %v", err)
	}
	if _, err := KeyFingerprint(string(pemKey), "wrong"); err == nil {
		t.Error("expected error for wrong passphrase, got nil")
	}
}

func TestKeyFingerprint_invalid(t *testing.T) {
	if _, err := KeyFingerprint("not a key", ""); err == nil {
		t.Error("expected error for invalid key, got nil")
	}
}

func TestMaterializeKey_roundtrip_unencrypted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pemKey := genEd25519PEM(t, "")

	path, cleanup, err := materializeKey(string(pemKey), "")
	if err != nil {
		t.Fatalf("materializeKey: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	if _, err := ssh.ParsePrivateKey(data); err != nil {
		t.Fatalf("materialized key not parseable: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("expected 0600 perms, got %o", perm)
	}

	// cleanup must remove the file.
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected file removed after cleanup, stat err = %v", err)
	}
}

func TestMaterializeKey_roundtrip_passphrase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pemKey := genEd25519PEM(t, "hunter2")

	path, cleanup, err := materializeKey(string(pemKey), "hunter2")
	if err != nil {
		t.Fatalf("materializeKey: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	// Must be UNENCRYPTED (parseable without passphrase).
	if _, err := ssh.ParsePrivateKey(data); err != nil {
		t.Fatalf("materialized key should be unencrypted and parseable: %v", err)
	}
}

func TestMaterializeKey_wrongPassphrase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pemKey := genEd25519PEM(t, "right")
	if _, _, err := materializeKey(string(pemKey), "wrong"); err == nil {
		t.Error("expected error for wrong passphrase, got nil")
	}
}

func TestMaterializeKey_plainPathPassthrough(t *testing.T) {
	path, cleanup, err := materializeKey("/some/existing/key", "")
	if err != nil {
		t.Fatalf("materializeKey: %v", err)
	}
	defer cleanup()
	if path != "/some/existing/key" {
		t.Errorf("expected passthrough path, got %q", path)
	}
}

func TestMaterializeKey_empty(t *testing.T) {
	path, cleanup, err := materializeKey("", "")
	if err != nil {
		t.Fatalf("materializeKey: %v", err)
	}
	defer cleanup()
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}
