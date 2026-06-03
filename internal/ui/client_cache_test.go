package ui

import (
	"io"
	"testing"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

type fakeHostClient struct {
	closed int
}

func (f *fakeHostClient) Run(string) ([]byte, error) { return nil, nil }

func (f *fakeHostClient) RunWithStreams(string, io.Reader, io.Writer, io.Writer) error {
	return nil
}

func (f *fakeHostClient) Upload(string, string) error { return nil }

func (f *fakeHostClient) Download(string, string) error { return nil }

func (f *fakeHostClient) ListRemoteDir(string) ([]hostexec.RemoteFileEntry, error) {
	return nil, nil
}

func (f *fakeHostClient) StatRemote(string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}

func (f *fakeHostClient) MkdirAllRemote(string) error { return nil }

func (f *fakeHostClient) RemoveRemote(string, bool) error {
	return nil
}

func (f *fakeHostClient) Close() error {
	f.closed++
	return nil
}

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

func TestSSHClientCacheKey_differsByMetaSSHIdentityFile(t *testing.T) {
	base := hosts.Record{
		Provider:  "gcp",
		Name:      "n1",
		PrimaryIP: "10.0.0.1",
	}
	a := hosts.CloneWithMetaSSHIdentityFile(base, "~/.ssh/a")
	b := hosts.CloneWithMetaSSHIdentityFile(base, "~/.ssh/b")
	u := "deploy"
	if SSHClientCacheKey(u, a) == SSHClientCacheKey(u, b) {
		t.Fatal("expected different cache keys for different meta ssh_identity_file")
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

func TestClientCacheAcquireLeaseReleasesWithoutClosingCachedClient(t *testing.T) {
	c := NewClientCache()
	c.SetRegistry(&hostexec.StandardRegistry{})
	r := hosts.Record{Provider: "aws", Name: "x", PrimaryIP: "1.2.3.4"}
	key := SSHClientCacheKey("u", r)
	client := &fakeHostClient{}
	c.clients[key] = client

	lease, err := c.AcquireLease("u", r)
	if err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if lease.HostClient() != client {
		t.Fatal("expected cached client lease")
	}
	if got := c.leases[key]; got != 1 {
		t.Fatalf("expected one lease, got %d", got)
	}

	if err := lease.Close(); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if _, ok := c.leases[key]; ok {
		t.Fatal("expected lease ref to be removed")
	}
	if client.closed != 0 {
		t.Fatalf("release should not close cached client, closed %d times", client.closed)
	}
	if c.clients[key] != client {
		t.Fatal("cached client should remain pooled after lease release")
	}
}
