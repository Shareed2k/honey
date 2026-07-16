package engine

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/moby/moby/client"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"github.com/shareed2k/honey/internal/sshclient"
	"golang.org/x/crypto/ssh"
)

// remoteDockerPluginShimDir is where honey-plugin-init is staged on a remote
// host before being bind-mounted into the plugin container. /tmp is writable
// on essentially every host; a host-level noexec mount on /tmp is irrelevant
// because the binary is exec'd inside the container (as its entrypoint), not
// on the host.
const remoteDockerPluginShimDir = "/tmp"

// isRemoteHostRecord reports whether r denotes a real remote host (as opposed
// to the synthetic local `host: "_"` record or an explicit localhost). Mirrors
// the local/remote split in step_docker.go's ExecuteStream.
func isRemoteHostRecord(r hosts.Record) bool {
	switch r.PrimaryIP {
	case "-", "127.0.0.1", "localhost", "":
		return false
	default:
		return true
	}
}

// dockerPluginSSHBackend is the remote plugins.DockerBackend: the plugin's
// shim-container runs on the target host's Docker daemon (the Engine API is
// tunneled over the borrowed SSH connection), the honey-plugin-init binary is
// staged onto that host, and the shim's published loopback port is dialed
// through the same SSH connection. It owns the SSH-tunneled moby client (Close
// closes it) but NOT the borrowed *ssh.Client, whose lifetime the ClientCache
// owns.
type dockerPluginSSHBackend struct {
	cli    *client.Client
	ssh    *ssh.Client
	host   HostClient
	record hosts.Record

	stageOnce  sync.Once
	stagedPath string
	stageErr   error
}

func (b *dockerPluginSSHBackend) Client() *client.Client { return b.cli }

// DialShim tunnels to the shim's published loopback port on the remote daemon
// host through the borrowed SSH connection. ssh.Client.Dial takes no context;
// the address is the daemon host's own 127.0.0.1:<published-port>.
func (b *dockerPluginSSHBackend) DialShim(_ context.Context, network, address string) (net.Conn, error) {
	return b.ssh.Dial(network, address)
}

func (b *dockerPluginSSHBackend) Close() error { return b.cli.Close() }

// ShimHostPath stages honey-plugin-init onto the remote host once (idempotently
// across calls) and returns its remote path.
func (b *dockerPluginSSHBackend) ShimHostPath(ctx context.Context) (string, error) {
	b.stageOnce.Do(func() { b.stagedPath, b.stageErr = b.stageShim(ctx) })
	return b.stagedPath, b.stageErr
}

func (b *dockerPluginSSHBackend) stageShim(ctx context.Context) (string, error) {
	arch, err := b.remoteArch()
	if err != nil {
		return "", err
	}
	localPath, err := plugins.LocateShimBinaryForArch(arch)
	if err != nil {
		return "", err
	}
	remotePath := remoteDockerPluginShimDir + "/honey-plugin-init-linux-" + arch
	node := NewHostClientTransferNode(b.record, b.host)
	if _, _, err := node.StageAgent(ctx, localPath, remotePath); err != nil {
		return "", fmt.Errorf("stage honey-plugin-init to %s: %w", targetLabel(b.record), err)
	}
	// StageAgent uploads but does not chmod; the shim must be executable to be
	// the container entrypoint.
	if _, err := b.host.Run("chmod +x " + shellQuote(remotePath)); err != nil {
		return "", fmt.Errorf("chmod honey-plugin-init on %s: %w", targetLabel(b.record), err)
	}
	return remotePath, nil
}

func (b *dockerPluginSSHBackend) remoteArch() (string, error) {
	out, err := b.host.Run("uname -m")
	if err != nil {
		return "", fmt.Errorf("detect remote architecture on %s: %w", targetLabel(b.record), err)
	}
	arch, err := normalizeDockerArch(string(out))
	if err != nil {
		return "", fmt.Errorf("%w on %s", err, targetLabel(b.record))
	}
	return arch, nil
}

// normalizeDockerArch maps a `uname -m` value to a Go GOARCH used in the
// honey-plugin-init-linux-<goarch> filename. Pure (no host dependency) so it's
// unit-tested directly.
func normalizeDockerArch(uname string) (string, error) {
	switch strings.TrimSpace(uname) {
	case "x86_64", "amd64":
		return "amd64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported remote architecture %q for docker plugin", strings.TrimSpace(uname))
	}
}

// newDockerPluginSSHBackendFactory returns a factory that builds a remote
// DockerBackend for r on demand. The DockerHostSession invokes it only once
// per (plugin, host), so the SSH borrow + moby-client construction happen once
// per host, not per call. runAs, when non-empty, makes the tunneled Engine API
// run as that user via sudo (see dockerprovider), matching the docker step.
func newDockerPluginSSHBackendFactory(ctx context.Context, cache *ClientCache, sshUser, runAs string, r hosts.Record) func() (plugins.DockerBackend, error) {
	return func() (plugins.DockerBackend, error) {
		honeyClient, err := cache.GetOrDial(sshUser, r)
		if err != nil {
			return nil, fmt.Errorf("ssh dial: %w", err)
		}
		sshClient, err := sshclient.LeafSSHFromClient(honeyClient)
		if err != nil {
			return nil, err
		}
		socketPath := "/var/run/docker.sock"
		if s := strings.TrimSpace(r.Meta["docker_socket"]); s != "" {
			socketPath = s
		}
		bc := dockerprovider.BackendConfig{SSHUser: sshUser, Socket: socketPath, RunAs: runAs}
		apiOpts := dockerprovider.APIClientOptions{SSHUser: sshUser, BorrowedSSH: sshClient, VMRecord: &r}
		mCli, err := dockerprovider.NewAPIClient(ctx, bc, apiOpts)
		if err != nil {
			return nil, fmt.Errorf("moby client: %w", err)
		}
		return &dockerPluginSSHBackend{cli: mCli, ssh: sshClient, host: honeyClient, record: r}, nil
	}
}

// dockerPluginHostKey is the per-host key the DockerHostSession uses to reuse
// one shim-container per host within a run.
func dockerPluginHostKey(r hosts.Record) string {
	return r.Provider + "|" + r.Name + "|" + r.PrimaryIP
}
