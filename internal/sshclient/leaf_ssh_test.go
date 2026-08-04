package sshclient

import (
	"context"
	"io"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/hostexec"
)

// fakeLeafProvider embeds the full hostexec.HostClient stand-in and adds
// LeafSSH() — proves LeafSSHFromClient's interface check works for any
// wrapper exposing LeafSSH(), not just *HoneyClient (e.g.
// proxmoxprovider's hybridQEMUClient).
type fakeLeafProvider struct {
	fakeNoLeafClient
	leaf *ssh.Client
}

func (f fakeLeafProvider) LeafSSH() *ssh.Client { return f.leaf }

// fakeNoLeafClient implements hostexec.HostClient but not leafSSHProvider —
// the "genuinely can't provide SSH" case (e.g. a pure QEMU-guest-agent
// client), which must still error, not panic.
type fakeNoLeafClient struct{}

func (fakeNoLeafClient) Run(string) ([]byte, error) { return nil, nil }
func (fakeNoLeafClient) RunWithStreams(string, io.Reader, io.Writer, io.Writer) error {
	return nil
}
func (fakeNoLeafClient) Upload(string, string) error   { return nil }
func (fakeNoLeafClient) Download(string, string) error { return nil }
func (fakeNoLeafClient) ListRemoteDir(string) ([]hostexec.RemoteFileEntry, error) {
	return nil, nil
}

func (fakeNoLeafClient) StatRemote(string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}
func (fakeNoLeafClient) MkdirAllRemote(string) error     { return nil }
func (fakeNoLeafClient) RemoveRemote(string, bool) error { return nil }
func (fakeNoLeafClient) StartLocalForward(context.Context, string, int, string, int) (string, int, func(), error) {
	return "", 0, nil, nil
}

func (fakeNoLeafClient) StartRemoteForward(context.Context, string, int, string, int) (string, func(), error) {
	return "", nil, nil
}

func (fakeNoLeafClient) StartDynamicForward(context.Context, string, int) (string, int, func(), error) {
	return "", 0, nil, nil
}

func (fakeNoLeafClient) StartUDPRelay(context.Context, string, int, string, int, bool) (string, int, func(), error) {
	return "", 0, nil, nil
}

func (fakeNoLeafClient) StartTunForward(context.Context, string, string, int, int, int) (string, func(), error) {
	return "", nil, nil
}

func (fakeNoLeafClient) StartLocalSocketForward(context.Context, string, string) (string, func(), error) {
	return "", nil, nil
}

func (fakeNoLeafClient) StartLocalTCPToSocketForward(context.Context, string, int, string) (string, int, func(), error) {
	return "", 0, nil, nil
}
func (fakeNoLeafClient) Close() error { return nil }

// TestLeafSSHFromClient_HoneyClientNil pins existing behavior: a nil-leaf
// *HoneyClient still errors (regression guard — *HoneyClient satisfying the
// new leafSSHProvider interface must not change this outcome).
func TestLeafSSHFromClient_HoneyClientNil(t *testing.T) {
	var hc *HoneyClient
	if _, err := LeafSSHFromClient(hc); err == nil {
		t.Fatal("expected error for a HoneyClient with no leaf ssh.Client, got nil")
	}
}

// TestLeafSSHFromClient_InterfaceMatch proves the generalized interface
// check accepts ANY wrapper implementing LeafSSH() *ssh.Client — not just
// *HoneyClient — e.g. proxmoxprovider's hybridQEMUClient.
func TestLeafSSHFromClient_InterfaceMatch(t *testing.T) {
	want := &ssh.Client{}
	got, err := LeafSSHFromClient(fakeLeafProvider{leaf: want})
	if err != nil {
		t.Fatalf("LeafSSHFromClient: %v", err)
	}
	if got != want {
		t.Errorf("LeafSSHFromClient returned %p, want %p", got, want)
	}
}

// TestLeafSSHFromClient_NoLeafProvider proves a HostClient that can't expose
// SSH at all (no LeafSSH method) errors cleanly, not panics.
func TestLeafSSHFromClient_NoLeafProvider(t *testing.T) {
	if _, err := LeafSSHFromClient(fakeNoLeafClient{}); err == nil {
		t.Fatal("expected error for a client with no LeafSSH method, got nil")
	}
}

// TestLeafSSHFromClient_ProviderReturnsNil proves a leafSSHProvider whose
// LeafSSH() itself returns nil (e.g. hybridQEMUClient wrapping a non-SSH
// client, mirrored in proxmoxprovider's own test) still errors rather than
// returning a nil *ssh.Client with a nil error.
func TestLeafSSHFromClient_ProviderReturnsNil(t *testing.T) {
	if _, err := LeafSSHFromClient(fakeLeafProvider{leaf: nil}); err == nil {
		t.Fatal("expected error when LeafSSH() itself returns nil, got nil")
	}
}
