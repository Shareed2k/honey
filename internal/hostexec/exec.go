// Package hostexec defines the execution surface (HostClient, Executor) shared by
// the TUI, web server, CUE runner, and provider-specific transports.
package hostexec

import (
	"context"
	"io"
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
	Close() error
}

// Executor creates HostClients and runs interactive SSH-style sessions or tunnels.
type Executor interface {
	Dial(user string, r hosts.Record) (HostClient, error)
	RunInteractive(user string, r hosts.Record) error
	RunTunnel(ctx context.Context, user string, r hosts.Record, localFwd string, out io.Writer) error
}
