package dockerprovider

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/moby/moby/client"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// BackendConfig is the runtime settings for one docker backend (search + dial).
type BackendConfig struct {
	Name          string
	Host          string
	ViaLocal      string
	ViaSSH        config.DockerViaSSH
	Socket        string
	Platform      string
	SSHUser       string
	RunAs         string
	LocalBackends []config.LocalBackend
	Mode          string
	AllContainers bool
	TLSVerify     bool
	CACert        string
	Cert          string
	Key           string
}

// BackendConfigFromYAML converts a config entry to BackendConfig.
func BackendConfigFromYAML(e config.DockerBackend, locals []config.LocalBackend, sshUser string) BackendConfig {
	return BackendConfig{
		Name:          strings.TrimSpace(e.Name),
		Host:          strings.TrimSpace(e.Host),
		ViaLocal:      strings.TrimSpace(e.ViaLocal),
		ViaSSH:        e.ViaSSH,
		Socket:        strings.TrimSpace(e.Socket),
		Platform:      strings.TrimSpace(e.Platform),
		SSHUser:       strings.TrimSpace(sshUser),
		RunAs:         strings.TrimSpace(e.RunAs),
		LocalBackends: locals,
		Mode:          strings.TrimSpace(e.Mode),
		AllContainers: e.AllContainers,
		TLSVerify:     e.TLSVerify,
		CACert:        strings.TrimSpace(e.CACert),
		Cert:          strings.TrimSpace(e.Cert),
		Key:           strings.TrimSpace(e.Key),
	}
}

// UsesHoneySSH reports whether this backend dials Docker via Honey SSH + remote socket.
func (b BackendConfig) UsesHoneySSH() bool {
	if strings.TrimSpace(b.ViaSSH.Host) != "" {
		return true
	}
	return strings.TrimSpace(b.ViaLocal) != ""
}

// ResolvedHost returns the host URI stored on records for Moby-direct backends.
func (b BackendConfig) ResolvedHost() string {
	if b.UsesHoneySSH() {
		hop, ok, err := ResolveSSHHop(b, nil)
		if err == nil && ok {
			return hop.RecordHostURI()
		}
	}
	if strings.TrimSpace(b.Host) != "" {
		return strings.TrimSpace(b.Host)
	}
	return "env"
}

// APIClientOptions optional SSH borrow and user for honey-ssh transport.
type APIClientOptions struct {
	SSHUser      string
	BorrowedSSH  *ssh.Client
	OwnsSSH      bool // if true, close SSH on client close
	VMRecord     *hosts.Record
	DiscoverOpts *DiscoverOpts
}

// DiscoverOpts configures auto-discovery pass (feature-flag only).
type DiscoverOpts struct {
	Socket   string
	Platform string
	RunAs    string
}

// NewAPIClient builds a Moby client for the given backend settings.
func NewAPIClient(ctx context.Context, b BackendConfig, opts APIClientOptions) (*client.Client, error) {
	if b.UsesHoneySSH() || opts.VMRecord != nil {
		return newHoneySSHAPIClient(ctx, b, opts)
	}
	return newMobyHostAPIClient(b)
}

func newMobyHostAPIClient(b BackendConfig) (*client.Client, error) {
	host := strings.TrimSpace(b.Host)
	var cOpts []client.Opt
	if host == "" {
		cOpts = append(cOpts, client.FromEnv)
	} else {
		cOpts = append(cOpts, client.WithHost(host))
		if strings.HasPrefix(host, "tcp://") || strings.HasPrefix(host, "https://") {
			if b.TLSVerify && b.CACert != "" {
				cOpts = append(cOpts, client.WithTLSClientConfig(b.CACert, b.Cert, b.Key))
			} else if !b.TLSVerify {
				cOpts = append(cOpts, client.WithTLSClientConfig("", "", ""))
			}
		}
	}
	return client.New(cOpts...)
}

func newHoneySSHAPIClient(ctx context.Context, b BackendConfig, opts APIClientOptions) (*client.Client, error) {
	_ = ctx
	hop, ok, err := ResolveSSHHop(b, opts.VMRecord)
	if !ok || err != nil {
		return nil, fmt.Errorf("resolve ssh hop: %w", err)
	}

	platform := NormalizePlatform(b.Platform)
	if opts.DiscoverOpts != nil {
		if s := strings.TrimSpace(opts.DiscoverOpts.Platform); s != "" {
			platform = NormalizePlatform(s)
		}
	}
	socket := b.Socket
	if opts.DiscoverOpts != nil && strings.TrimSpace(opts.DiscoverOpts.Socket) != "" {
		socket = opts.DiscoverOpts.Socket
	}
	dialP, err := SocketDialParams(socket, platform)
	if err != nil {
		return nil, err
	}

	var sshClient *ssh.Client
	var cleanup func()
	if opts.BorrowedSSH != nil {
		sshClient = opts.BorrowedSSH
		cleanup = func() {}
	} else if borrowed, ok := hostexec.BorrowDockerSSH(opts.SSHUser, hop.HopRecord()); ok {
		sshClient = borrowed
		cleanup = func() {}
	} else {
		user := strings.TrimSpace(opts.SSHUser)
		if user == "" {
			user = strings.TrimSpace(b.SSHUser)
		}
		sshClient, cleanup, err = DialSSH(hop, user)
		if err != nil {
			return nil, err
		}
		_ = opts.OwnsSSH
	}
	_ = cleanup // caller closes moby client only; SSH kept open unless owns

	runAs := resolveHoneySSHRunAs(b, opts)
	cOpts := []client.Opt{
		client.WithHost(dialP.HostURL),
		client.WithDialContext(func(ctx context.Context, network, addr string) (net.Conn, error) {
			_ = network
			_ = addr
			return dialHoneyTransportConn(ctx, sshClient, dialP, runAs)
		}),
	}
	return client.New(cOpts...)
}

func resolveHoneySSHRunAs(b BackendConfig, opts APIClientOptions) string {
	if opts.DiscoverOpts != nil {
		if s := strings.TrimSpace(opts.DiscoverOpts.RunAs); s != "" {
			return s
		}
	}
	return strings.TrimSpace(b.RunAs)
}

// NewAPIClientFromRuntime builds a client from hostexec runtime config.
func NewAPIClientFromRuntime(ctx context.Context, rt hostexec.DockerBackendRuntime, opts APIClientOptions) (*client.Client, error) {
	bc := BackendConfig{
		Name:          rt.Name,
		Host:          rt.Host,
		ViaLocal:      rt.ViaLocal,
		ViaSSH:        rt.ViaSSH,
		Socket:        rt.Socket,
		Platform:      rt.Platform,
		SSHUser:       opts.SSHUser,
		RunAs:         rt.RunAs,
		LocalBackends: rt.LocalBackends,
		TLSVerify:     rt.TLSVerify,
		CACert:        rt.CACert,
		Cert:          rt.Cert,
		Key:           rt.Key,
	}
	if opts.VMRecord != nil {
		return NewAPIClient(ctx, bc, opts)
	}
	if bc.UsesHoneySSH() || strings.HasPrefix(strings.TrimSpace(rt.Transport), "honey_ssh") {
		return NewAPIClient(ctx, bc, opts)
	}
	return newMobyHostAPIClient(bc)
}

// ContainerIDFromRecord returns the target container ID for exec/file ops.
func ContainerIDFromRecord(containerID string) (string, error) {
	id := strings.TrimSpace(containerID)
	if id == "" {
		return "", fmt.Errorf("missing container_id in record metadata")
	}
	return id, nil
}

// NormalizeMode returns containers, swarm, or both.
func NormalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "swarm", "both":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "containers"
	}
}

// ListOptionsForBackend returns container list options for search.
func ListOptionsForBackend(all bool) client.ContainerListOptions {
	return client.ContainerListOptions{All: all}
}

// RecordMetaBase returns common docker record meta for a backend connection.
func RecordMetaBase(bc BackendConfig, hop SSHHop, discover bool) map[string]string {
	hostURI := bc.ResolvedHost()
	if bc.UsesHoneySSH() || discover {
		hostURI = hop.RecordHostURI()
	}
	meta := map[string]string{
		"docker_host":      hostURI,
		"docker_backend":   strings.TrimSpace(bc.Name),
		"docker_transport": "moby",
	}
	if bc.UsesHoneySSH() || discover {
		meta["docker_transport"] = "honey_ssh"
	}
	if discover {
		meta["docker_discover"] = "1"
	}
	if ra := strings.TrimSpace(bc.RunAs); ra != "" {
		meta["docker_run_as"] = ra
	}
	if u := strings.TrimSpace(bc.SSHUser); u != "" {
		meta["docker_ssh_user"] = u
	}
	return meta
}
