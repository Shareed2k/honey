package sshclient

import (
	"testing"
)

func TestParseSSHGOutput_sample(t *testing.T) {
	const sample = `host example.com
user deploy
hostname 10.0.0.5
port 2222
stricthostkeychecking ask
proxyjump bastion
identityfile ~/.ssh/id_ed25519
identityfile ~/.ssh/id_rsa
userknownhostsfile /tmp/kh1 /tmp/kh2
globalknownhostsfile /etc/ssh/ssh_known_hosts
`

	p, ok := parseSSHGOutput([]byte(sample))
	if !ok {
		t.Fatal("expected ok")
	}
	if p.user != "deploy" {
		t.Fatalf("user: %q", p.user)
	}
	if p.hostname != "10.0.0.5" {
		t.Fatalf("hostname: %q", p.hostname)
	}
	if p.port != "2222" {
		t.Fatalf("port: %q", p.port)
	}
	if p.proxyJump != "bastion" {
		t.Fatalf("proxyjump: %q", p.proxyJump)
	}
	if p.strictHostKeyChecking != "ask" {
		t.Fatalf("strict: %q", p.strictHostKeyChecking)
	}
	if len(p.identityFiles) != 2 || p.identityFiles[0] != "~/.ssh/id_ed25519" || p.identityFiles[1] != "~/.ssh/id_rsa" {
		t.Fatalf("identityfiles: %#v", p.identityFiles)
	}
	if len(p.userKnownHosts) != 2 || p.userKnownHosts[0] != "/tmp/kh1" || p.userKnownHosts[1] != "/tmp/kh2" {
		t.Fatalf("userknownhosts: %#v", p.userKnownHosts)
	}
	if len(p.globalKnownHosts) != 1 || p.globalKnownHosts[0] != "/etc/ssh/ssh_known_hosts" {
		t.Fatalf("globalknownhosts: %#v", p.globalKnownHosts)
	}
}

func TestParseSSHGOutput_noHostname(t *testing.T) {
	_, ok := parseSSHGOutput([]byte("user root\nport 22\n"))
	if ok {
		t.Fatal("expected not ok without hostname")
	}
}

func TestSSHGDestination(t *testing.T) {
	if got := sshGDestination("u", "10.0.0.1"); got != "u@10.0.0.1" {
		t.Fatalf("got %q", got)
	}
	if got := sshGDestination("", "10.0.0.1"); got != "10.0.0.1" {
		t.Fatalf("got %q", got)
	}
}

func TestResolvedFromOpenSSH_userOverrideWins(t *testing.T) {
	p := openSSHGParsed{user: "ubuntu", hostname: "h.example", port: "22"}
	r := resolvedFromOpenSSH("alias", "deploy", p)
	if r.user != "deploy" {
		t.Fatalf("user: %q", r.user)
	}
	if r.host != "h.example" {
		t.Fatalf("host: %q", r.host)
	}
	if r.port != 22 {
		t.Fatalf("port: %d", r.port)
	}
}
