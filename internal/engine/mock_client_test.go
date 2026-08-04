package engine

import (
	"context"
	"io"
	"net"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// MockHostClient ...
type MockHostClient struct {
	RemoteCmd string
}

func (m *MockHostClient) SupportsKVTunnel() bool { return false }

func (m *MockHostClient) Close() error { return nil }
func (m *MockHostClient) Output(cmd string, _ map[string]string) ([]byte, error) {
	m.RemoteCmd = cmd
	return nil, nil
}

func (m *MockHostClient) OutputWithStderr(cmd string, _ map[string]string) ([]byte, []byte, error) {
	m.RemoteCmd = cmd
	return nil, nil, nil
}

func (m *MockHostClient) Download(_ string, _ string) error { return nil }

func (m *MockHostClient) Upload(_ string, _ string) error { return nil }

func (m *MockHostClient) UploadContent(_ []byte, _ string, _ uint32) error { return nil }

func (m *MockHostClient) ListRemoteDir(_ string) ([]hostexec.RemoteFileEntry, error) {
	return nil, nil
}

func (m *MockHostClient) StatRemotePath(_ string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}

func (m *MockHostClient) StatRemote(_ string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}
func (m *MockHostClient) MkdirAllRemote(_ string) error                           { return nil }
func (m *MockHostClient) RemoveRemote(_ string, _ bool) error                     { return nil }
func (m *MockHostClient) InteractiveTerminal(_ string, _ map[string]string) error { return nil }
func (m *MockHostClient) RunWithStreams(_ string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	return nil
}

func (m *MockHostClient) Run(cmd string) ([]byte, error) {
	m.RemoteCmd = cmd
	return nil, nil
}

func (m *MockHostClient) StartLocalForward(_ context.Context, _ string, _ int, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, nil
}

func (m *MockHostClient) StartRemoteForward(_ context.Context, _ string, _ int, _ string, _ int) (remAddr string, stop func(), err error) {
	return "", nil, nil
}

func (m *MockHostClient) StartDynamicForward(_ context.Context, _ string, _ int) (host string, port int, stop func(), err error) {
	return "", 0, nil, nil
}

func (m *MockHostClient) StartUDPRelay(_ context.Context, _ string, _ int, _ string, _ int, _ bool) (host string, port int, stop func(), err error) {
	return "", 0, nil, nil
}

func (m *MockHostClient) StartTunForward(_ context.Context, _ string, _ string, _ int, _, _ int) (tunName string, stop func(), err error) {
	return "", nil, nil
}

func (m *MockHostClient) StartLocalSocketForward(_ context.Context, _ string, _ string) (localPath string, stop func(), err error) {
	return "", nil, nil
}

func (m *MockHostClient) StartLocalTCPToSocketForward(_ context.Context, _ string, _ int, _ string) (host string, port int, stop func(), err error) {
	return "", 0, nil, nil
}

// MockRegistry and MockExecutor for testing

type MockExecutor struct {
	Client hostexec.HostClient
}

func (m *MockExecutor) Dial(_ string, _ hosts.Record) (hostexec.HostClient, error) {
	return m.Client, nil
}
func (m *MockExecutor) RunInteractive(_ string, _ hosts.Record) error { return nil }
func (m *MockExecutor) RunTunnel(_ context.Context, _ string, _ hosts.Record, _ string, _ io.Writer) error {
	return nil
}

func (m *MockExecutor) DialUpstream(_ context.Context, _ string, _ hosts.Record, _ string) (net.Conn, error) {
	return nil, nil
}

type MockRegistry struct {
	Client hostexec.HostClient
}

func (m *MockRegistry) ForRecord(_ hosts.Record) hostexec.Executor {
	return &MockExecutor{Client: m.Client}
}
func (m *MockRegistry) Reconfigure(_ *config.File) {}
func (m *MockRegistry) RunSSHTunnel(_ context.Context, _, _ string, _ int, _ string, _ io.Writer) error {
	return nil
}
func (m *MockRegistry) BorrowSSH(_ string, _ hosts.Record) (any, bool) { return nil, false }
