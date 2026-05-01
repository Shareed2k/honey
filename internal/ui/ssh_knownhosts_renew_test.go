package ui

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestHoneySSHAutoRenewStaleHostKeys(t *testing.T) {
	t.Setenv("HONEY_SSH_RENEW_STALE_HOST_KEYS", "")
	if !honeySSHAutoRenewStaleHostKeys() {
		t.Fatal("expected true when unset (default on)")
	}
	t.Setenv("HONEY_SSH_RENEW_STALE_HOST_KEYS", "0")
	if honeySSHAutoRenewStaleHostKeys() {
		t.Fatal("expected false for 0")
	}
	t.Setenv("HONEY_SSH_RENEW_STALE_HOST_KEYS", "yes")
	if !honeySSHAutoRenewStaleHostKeys() {
		t.Fatal("expected true for non-opt-out values")
	}
}

func TestKnownHostsRemovalCandidates(t *testing.T) {
	addr, err := net.ResolveTCPAddr("tcp", "192.0.2.1:22")
	if err != nil {
		t.Fatal(err)
	}
	got := knownHostsRemovalCandidates("192.0.2.1", addr)
	if len(got) < 1 {
		t.Fatalf("%#v", got)
	}
}

func TestCanMutateKnownHostsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "kh")
	if err := os.WriteFile(p, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !canMutateKnownHostsFile(p) {
		t.Fatal("expected writable file")
	}
	if canMutateKnownHostsFile(filepath.Join(dir, "missing")) {
		t.Fatal("expected false for missing")
	}
}
