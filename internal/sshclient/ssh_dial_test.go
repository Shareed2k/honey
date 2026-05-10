package sshclient

import (
	"strings"
	"testing"
)

func TestSSHKeyscanCLIPrompt(t *testing.T) {
	if g := sshKeyscanCLIPrompt("10.201.0.147:22"); !strings.Contains(g, "-H 10.201.0.147") || strings.Contains(g, "10.201.0.147:22") {
		t.Fatalf("expected bare host for port 22, got %q", g)
	}
	if g := sshKeyscanCLIPrompt("10.201.0.147:2222"); !strings.Contains(g, "-p 2222") || !strings.Contains(g, "-H 10.201.0.147") {
		t.Fatalf("expected -p for non-22, got %q", g)
	}
}

func TestParseLocalForward(t *testing.T) {
	lp, rh, rp, err := parseLocalForward("8080:db.internal:5432")
	if err != nil {
		t.Fatal(err)
	}
	if lp != "8080" || rh != "db.internal" || rp != "5432" {
		t.Fatalf("got %q %q %q", lp, rh, rp)
	}
	_, _, _, err = parseLocalForward("bad")
	if err == nil {
		t.Fatal("expected error")
	}
}
