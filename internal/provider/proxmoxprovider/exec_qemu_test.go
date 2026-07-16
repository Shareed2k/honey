package proxmoxprovider

import (
	"context"
	"io"
	"testing"

	"github.com/shareed2k/honey/internal/hostexec"
)

// fakeHostClient is a minimal hostexec.HostClient stand-in that is NOT a
// *sshclient.HoneyClient, for proving hybridQEMUClient.LeafSSH() degrades to
// nil rather than panicking or misreporting when its ssh field isn't
// SSH-backed (shouldn't happen in practice — dialHybridQEMU always assigns a
// real *sshclient.HoneyClient — but LeafSSH must stay safe regardless).
type fakeHostClient struct{}

func (fakeHostClient) Run(string) ([]byte, error) { return nil, nil }
func (fakeHostClient) RunWithStreams(string, io.Reader, io.Writer, io.Writer) error {
	return nil
}
func (fakeHostClient) Upload(string, string) error   { return nil }
func (fakeHostClient) Download(string, string) error { return nil }
func (fakeHostClient) ListRemoteDir(string) ([]hostexec.RemoteFileEntry, error) {
	return nil, nil
}

func (fakeHostClient) StatRemote(string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}
func (fakeHostClient) MkdirAllRemote(string) error     { return nil }
func (fakeHostClient) RemoveRemote(string, bool) error { return nil }
func (fakeHostClient) StartLocalForward(context.Context, string, int, string, int) (string, int, func(), error) {
	return "", 0, nil, nil
}

func (fakeHostClient) StartRemoteForward(context.Context, string, int, string, int) (string, func(), error) {
	return "", nil, nil
}

func (fakeHostClient) StartDynamicForward(context.Context, string, int) (string, int, func(), error) {
	return "", 0, nil, nil
}

func (fakeHostClient) StartUDPRelay(context.Context, string, int, string, int, bool) (string, int, func(), error) {
	return "", 0, nil, nil
}

func (fakeHostClient) StartTunForward(context.Context, string, string, int, int, int) (string, func(), error) {
	return "", nil, nil
}
func (fakeHostClient) Close() error { return nil }

// TestHybridQEMUClient_LeafSSH_NonHoneyClient proves LeafSSH() returns nil
// (not a panic) when the wrapped ssh field isn't a *sshclient.HoneyClient —
// the defensive branch of the type assertion. The real, always-true-in-
// practice case (ssh field genuinely holds a *sshclient.HoneyClient, from
// dialHybridQEMU's sshclient.DialHoneyClient call) is covered by a live run
// against a real hybrid-exec-mode Proxmox QEMU VM, not a synthetic SSH
// fixture here.
func TestHybridQEMUClient_LeafSSH_NonHoneyClient(t *testing.T) {
	h := &hybridQEMUClient{ssh: fakeHostClient{}}
	if got := h.LeafSSH(); got != nil {
		t.Errorf("LeafSSH() = %v, want nil for a non-HoneyClient ssh field", got)
	}
}
