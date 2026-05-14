package sshclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitCommaNonEmpty(t *testing.T) {
	t.Parallel()
	if got := splitCommaNonEmpty(""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
	if got := splitCommaNonEmpty("  a , b , "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestDefaultSSHIdentityKeyBaseNames(t *testing.T) {
	t.Parallel()
	names := defaultSSHIdentityKeyBaseNames()
	if len(names) < 5 {
		t.Fatalf("short: %v", names)
	}
	want := []string{"id_ed25519", "id_rsa", "id_ecdsa", "google_compute_engine", "id_dsa"}
	for i, w := range want {
		if i >= len(names) || names[i] != w {
			t.Fatalf("index %d want %q got %v", i, w, names)
		}
	}
}

func TestIdentityPathsFromHoneyEnv(t *testing.T) {
	t.Setenv(honeySSHIdentityFilesEnv, "")
	if p, err := identityPathsFromHoneyEnv(); err != nil || len(p) != 0 {
		t.Fatalf("empty env: %v %v", p, err)
	}
	dir := t.TempDir()
	k1 := filepath.Join(dir, "one")
	k2 := filepath.Join(dir, "two")
	if err := os.WriteFile(k1, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k2, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(honeySSHIdentityFilesEnv, k1+" , "+k2)
	got, err := identityPathsFromHoneyEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != k1 || got[1] != k2 {
		t.Fatalf("got %v", got)
	}
}

func TestBuildAuthWithIdentityFiles_googleComputeDefault(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("testdata/id_ed25519")
	if err != nil {
		t.Fatalf("read test key: %v", err)
	}
	gceKey := filepath.Join(sshDir, "google_compute_engine")
	if err := os.WriteFile(gceKey, src, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "") // avoid agent in environments that inherit one

	auth, err := buildAuthWithIdentityFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(auth) == 0 {
		t.Fatal("expected at least one auth method from google_compute_engine default")
	}
}

func TestBuildAuthWithIdentityFiles_envExtra(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(home, "extra_key")
	src, err := os.ReadFile("testdata/id_ed25519")
	if err != nil {
		t.Fatalf("read test key: %v", err)
	}
	if err := os.WriteFile(keyPath, src, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv(honeySSHIdentityFilesEnv, keyPath)

	auth, err := buildAuthWithIdentityFiles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(auth) == 0 {
		t.Fatal("expected auth from HONEY_SSH_IDENTITY_FILES")
	}
}

func TestBuildAuthWithIdentityFiles_noAuthError(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SSH_AUTH_SOCK", "")
	t.Setenv(honeySSHIdentityFilesEnv, "")

	_, err := buildAuthWithIdentityFiles(nil)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"no ssh auth", "honey_ssh_identity_files", "match", "ssh_auth_sock"} {
		if !strings.Contains(msg, needle) {
			t.Fatalf("error should mention %q: %s", needle, err.Error())
		}
	}
}
