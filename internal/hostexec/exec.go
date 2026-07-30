// Package hostexec defines the execution surface (HostClient, Executor) shared by
// the TUI, web server, CUE runner, and provider-specific transports.
package hostexec

import (
	"context"
	"io"
	"net"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
)

// RemoteFileEntry describes one filesystem object on the remote host.
type RemoteFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modified_at"`
}

// HostClient defines running commands and file operations on a single host.
type HostClient interface {
	Run(cmd string) ([]byte, error)
	// RunWithStreams runs a remote command with stdin/stdout/stderr wired through.
	// stderr may be nil to discard remote stderr.
	RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error
	Upload(localPath, remotePath string) error
	Download(remotePath, localPath string) error
	ListRemoteDir(path string) ([]RemoteFileEntry, error)
	StatRemote(path string) (RemoteFileEntry, error)
	MkdirAllRemote(path string) error
	RemoveRemote(path string, recursive bool) error

	// Tunneling methods
	StartLocalForward(ctx context.Context, bind string, localPort int, remoteHost string, remotePort int) (host string, port int, stop func(), err error)
	StartRemoteForward(ctx context.Context, remoteBind string, remoteListen int, localHost string, localTarget int) (remAddr string, stop func(), err error)
	StartDynamicForward(ctx context.Context, bind string, localPort int) (host string, port int, stop func(), err error)
	StartUDPRelay(ctx context.Context, bind string, localPort int, remoteHost string, remotePort int, useSocat bool) (host string, port int, stop func(), err error)
	StartTunForward(ctx context.Context, user string, alias string, sshPort int, tunLocal, tunRemote int) (tunName string, stop func(), err error)

	Close() error
}

// Executor creates HostClients and runs interactive SSH-style sessions or tunnels.
type Executor interface {
	Dial(user string, r hosts.Record) (HostClient, error)
	RunInteractive(user string, r hosts.Record) error
	RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error
	DialUpstream(ctx context.Context, user string, r hosts.Record, address string) (net.Conn, error)
}

// InteractiveStreamer is optionally implemented by executors that can run an
// interactive TTY over caller-provided streams — the web terminal's WebSocket
// pipes, or a recorded session — instead of the process's own os.Stdin/Stdout.
// honeyprovider forwards it over the mesh (the upstream server dispatches to the
// right native shell); native providers (docker/k8s) run it locally. resize
// carries [cols, rows] pairs and is closed by the caller when the session ends.
//
// It is a capability interface, resolved from Registry.ForRecord via a type
// assertion: a provider that has no interactive TTY simply does not implement
// it. This lets one seam serve the web/CLI/mesh terminal paths uniformly instead
// of each caller dispatching by record kind and down-casting to a concrete
// native client.
type InteractiveStreamer interface {
	RunInteractiveStreams(ctx context.Context, user string, r hosts.Record, stdin io.Reader, stdout io.Writer, cols, rows int, resize <-chan [2]int) error
}

// ProxyExecutor is optionally implemented by an executor that forwards a session
// to another node (e.g. the honey mesh) instead of running it on this node. When
// Registry.ForRecord resolves to a proxy, callers route the whole interactive
// session to it up front — ahead of any local provider-specific console — so a
// mesh-routed record is handled on the node that owns it. Native executors do not
// implement it (equivalently, IsProxy would be false), which is how a dispatcher
// tells "forward this elsewhere" from "run it here" without naming a provider or
// inspecting routing metadata.
type ProxyExecutor interface {
	IsProxy() bool
}

// IsProxy reports whether ex forwards sessions to another node (e.g. the honey
// mesh) rather than executing them locally — that is, it implements ProxyExecutor
// and says so. A nil executor or a native (local) executor returns false. It is
// the shared decision point for the web and TUI terminal dispatchers, which route
// a mesh-resolved record wholesale to its owning node before attempting any local
// provider console or native shell.
func IsProxy(ex Executor) bool {
	pe, ok := ex.(ProxyExecutor)
	return ok && pe.IsProxy()
}
