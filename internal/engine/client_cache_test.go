package engine

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// FakeHostClient ...
type FakeHostClient struct {
	RunFunc func(cmd string) ([]byte, error)

	closed int
}

func (f *FakeHostClient) Run(cmd string) ([]byte, error) {
	if f.RunFunc != nil {
		return f.RunFunc(cmd)
	}
	return []byte("hook ok"), nil
}

func (f *FakeHostClient) RunWithStreams(cmd string, _ io.Reader, stdout io.Writer, _ io.Writer) error {
	if f.RunFunc != nil {
		out, err := f.RunFunc(cmd)
		if stdout != nil {
			stdout.Write(out)
		}
		return err
	}
	return nil
}

func (f *FakeHostClient) Upload(string, string) error { return nil }

func (f *FakeHostClient) Download(string, string) error { return nil }

func (f *FakeHostClient) ListRemoteDir(string) ([]hostexec.RemoteFileEntry, error) {
	return nil, nil
}

func (f *FakeHostClient) StatRemote(string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}

func (f *FakeHostClient) MkdirAllRemote(string) error { return nil }

func (f *FakeHostClient) RemoveRemote(string, bool) error {
	return nil
}

func (f *FakeHostClient) Close() error {
	f.closed++
	return nil
}

// TestSSHClientCacheKey_differsByMetaSSHPort ...
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

// TestSSHClientCacheKey_differsByMetaSSHIdentityFile ...
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

// TestSSHClientCacheKey_sameWhenNoMetaPort ...
func TestSSHClientCacheKey_sameWhenNoMetaPort(t *testing.T) {
	r := hosts.Record{Provider: "aws", Name: "x", PrimaryIP: "1.2.3.4"}
	k1 := SSHClientCacheKey("u", r)
	k2 := SSHClientCacheKey("u", r)
	if k1 != k2 {
		t.Fatalf("expected stable cache key, got %q vs %q", k1, k2)
	}
}

// TestClientCacheAcquireLeaseReleasesWithoutClosingCachedClient ...
func TestClientCacheAcquireLeaseReleasesWithoutClosingCachedClient(t *testing.T) {
	c := NewClientCache()
	c.SetRegistry(&MockRegistry{})
	r := hosts.Record{Provider: "aws", Name: "x", PrimaryIP: "1.2.3.4"}
	key := SSHClientCacheKey("u", r)
	client := &FakeHostClient{}
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

// StartLocalForward starts a local port forward.
func (f *FakeHostClient) StartLocalForward(_ context.Context, _ string, _ int, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartRemoteForward starts a remote port forward.
func (f *FakeHostClient) StartRemoteForward(_ context.Context, _ string, _ int, _ string, _ int) (remAddr string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartDynamicForward starts a dynamic port forward.
func (f *FakeHostClient) StartDynamicForward(_ context.Context, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartUDPRelay starts a UDP relay.
func (f *FakeHostClient) StartUDPRelay(_ context.Context, _ string, _ int, _ string, _ int, _ bool) (host string, port int, stop func(), err error) {
	return "", 0, nil, fmt.Errorf("tunneling not supported on this transport")
}

// StartTunForward starts a TUN forward.
func (f *FakeHostClient) StartTunForward(_ context.Context, _ string, _ string, _ int, _, _ int) (tunName string, stop func(), err error) {
	return "", nil, fmt.Errorf("tunneling not supported on this transport")
}

func (f *FakeHostClient) StartLocalSocketForward(_ context.Context, _ string, _ string) (localPath string, stop func(), err error) {
	return "", nil, nil
}

func (f *FakeHostClient) StartLocalTCPToSocketForward(_ context.Context, _ string, _ int, _ string) (host string, port int, stop func(), err error) {
	return "", 0, nil, nil
}
