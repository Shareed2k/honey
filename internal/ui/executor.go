package ui

import (
	"fmt"
	"io"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
)

// HostClient defines the interface for executing commands and transferring files on a host.
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

// RemoteFileEntry describes one filesystem object on the remote host.
type RemoteFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modified_at"`
}

// Executor defines the interface for creating HostClients and running interactive sessions.
type Executor interface {
	Dial(user string, r hosts.Record) (HostClient, error)
	RunInteractive(user string, r hosts.Record) error
	RunTunnel(user string, r hosts.Record, localFwd string) error
}

// defaultSSHExecutor implements standard SSH execution using DialHoneyClient.
type defaultSSHExecutor struct{}

func (e defaultSSHExecutor) Dial(user string, r hosts.Record) (HostClient, error) {
	return DialHoneyClient(user, r.PrimaryIP)
}

func (e defaultSSHExecutor) RunInteractive(user string, r hosts.Record) error {
	return runSSHInteractive(user, r, nil)
}

func (e defaultSSHExecutor) RunTunnel(user string, r hosts.Record, localFwd string) error {
	return runTunnelGo(user, r.PrimaryIP, localFwd)
}

// DefaultExecutor is the default implementation for remote execution.
var DefaultExecutor Executor = defaultSSHExecutor{}

// GetExecutor returns the appropriate Executor for a host record.
func GetExecutor(r hosts.Record) Executor {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		// k8s pod executor will go here
		return k8sPodExecutor{}
	}
	return DefaultExecutor
}

// FormatTargetForDryRun returns a string describing how the target will be connected to.
func FormatTargetForDryRun(r hosts.Record) string {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return fmt.Sprintf("k8s_exec(ns=%s pod=%s)", r.Meta["namespace"], r.Meta["pod_name"])
	}
	return fmt.Sprintf("ip=%s", r.PrimaryIP)
}

type k8sPodExecutor struct{}
