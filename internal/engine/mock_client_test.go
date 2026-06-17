package engine_test

import (
	"io"

	"github.com/shareed2k/honey/internal/hostexec"
)

// MockHostClient ...
type MockHostClient struct {
	RemoteCmd string
}

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
