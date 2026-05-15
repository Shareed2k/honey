package ui

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestSSHClientCacheKey_differsByMetaSSHPort(t *testing.T) {
	base := hosts.Record{
		Provider:  "gcp",
		Name:      "n1",
		PrimaryIP: "10.0.0.1",
	}
	a := base
	a.Meta = map[string]string{"ssh_port": "22"}
	b := base
	b.Meta = map[string]string{"ssh_port": "2222"}
	u := "deploy"
	if SSHClientCacheKey(u, a) == SSHClientCacheKey(u, b) {
		t.Fatal("expected different cache keys for different meta ssh_port")
	}
}

func TestSSHClientCacheKey_sameWhenNoMetaPort(t *testing.T) {
	r := hosts.Record{Provider: "aws", Name: "x", PrimaryIP: "1.2.3.4"}
	k1 := SSHClientCacheKey("u", r)
	k2 := SSHClientCacheKey("u", r)
	if k1 != k2 {
		t.Fatalf("expected stable cache key, got %q vs %q", k1, k2)
	}
}
