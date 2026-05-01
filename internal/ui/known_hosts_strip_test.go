package ui

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestDialKhAddrs(t *testing.T) {
	addr, err := net.ResolveTCPAddr("tcp", "192.0.2.10:22")
	if err != nil {
		t.Fatal(err)
	}
	got, err := dialKhAddrs("", addr)
	if err != nil || len(got) != 1 || got[0].host != "192.0.2.10" || got[0].port != "22" {
		t.Fatalf("%+v %v", got, err)
	}
	got2, err := dialKhAddrs("192.0.2.10", addr)
	if err != nil || len(got2) != 1 {
		t.Fatalf("%+v %v", got2, err)
	}
}

func TestRewriteKnownHostsStrippingIPv4(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ssh.NewPublicKey(priv.Public())
	if err != nil {
		t.Fatal(err)
	}
	_, priv2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pub2, err := ssh.NewPublicKey(priv2.Public())
	if err != nil {
		t.Fatal(err)
	}
	line := knownhosts.Line([]string{"192.0.2.1"}, pub) + "\n" + knownhosts.Line([]string{"other.example.com"}, pub2) + "\n"

	dir := t.TempDir()
	p := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, err := net.ResolveTCPAddr("tcp", "192.0.2.1:22")
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := dialKhAddrs("", addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := rewriteKnownHostsStrippingAddrs(p, addrs)
	if err != nil || n != 1 {
		t.Fatalf("removed=%d err=%v", n, err)
	}
	out, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "192.0.2.1") {
		t.Fatalf("expected 192.0.2.1 line removed, got:\n%s", out)
	}
	if !strings.Contains(string(out), "other.example.com") {
		t.Fatalf("expected unrelated line kept:\n%s", out)
	}
}

func TestRewriteKnownHostsKeepsMalformed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "known_hosts")
	raw := "this is not a valid known_hosts line\n"
	if err := os.WriteFile(p, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:22")
	if err != nil {
		t.Fatal(err)
	}
	addrs, err := dialKhAddrs("", addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := rewriteKnownHostsStrippingAddrs(p, addrs)
	if err != nil || n != 0 {
		t.Fatalf("removed=%d err=%v", n, err)
	}
	out, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != raw {
		t.Fatalf("expected unchanged file")
	}
}
