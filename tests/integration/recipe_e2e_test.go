//go:build integration

package integration

import (
	"os"
	"path/filepath"

	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

type mockHostClient struct {
	outputs map[string]string
	errs    map[string]error
}

func (m *mockHostClient) Run(cmd string) ([]byte, error) {
	for k, err := range m.errs {
		if strings.Contains(cmd, k) {
			return nil, err
		}
	}
	for k, out := range m.outputs {
		if strings.Contains(cmd, k) {
			return []byte(out), nil
		}
	}
	return []byte("success"), nil
}

func (m *mockHostClient) RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	out, err := m.Run(cmd)
	if err != nil {
		return err
	}
	if stdout != nil {
		stdout.Write(out)
	}
	return nil
}

func (m *mockHostClient) Upload(_, _ string) error   { return nil }
func (m *mockHostClient) Download(_, _ string) error { return nil }
func (m *mockHostClient) ListRemoteDir(_ string) ([]hostexec.RemoteFileEntry, error) { return nil, nil }
func (m *mockHostClient) StatRemote(_ string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, nil
}
func (m *mockHostClient) MkdirAllRemote(_ string) error       { return nil }
func (m *mockHostClient) RemoveRemote(_ string, _ bool) error { return nil }
func (m *mockHostClient) Close() error                        { return nil }
func (m *mockHostClient) SupportsKVTunnel() bool              { return false }

func TestRecipeE2E_LinearExecution(t *testing.T) {
	reg := &testRegistry{
		Dialer: DialerFunc(func(user, host string, port int, keyFile string) (hostexec.HostClient, error) {
			return &mockHostClient{
				outputs: map[string]string{
					"echo hello E2E":            "hello E2E\n",
					"touch /tmp/e2e_test_file": "",
					"ls /tmp/e2e_test_file":    "/tmp/e2e_test_file\n",
				},
			}, nil
		}),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: "mockhost"}, 22)

	cueContent := `
recipe: {
	name: "test-linear"
	type: "linear"
	steps: [
		{
			host: "*"
			command: "echo hello E2E"
		},
		{
			host: "*"
			command: "touch /tmp/e2e_test_file"
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)

	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		SSHUser: "testuser",
		Execute: true,
		Reg:     reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.Contains(t, results[0].Output, "hello E2E")
	assert.True(t, results[1].Success)

	client, err := reg.Dialer.Dial("testuser", "mockhost", 22, "")
	require.NoError(t, err)
	defer client.Close()

	out, err := client.Run("ls /tmp/e2e_test_file")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/e2e_test_file\n", string(out))
}

func TestRecipeE2E_MultilineEnvVar(t *testing.T) {
	reg := &testRegistry{
		Dialer: DialerFunc(func(user, host string, port int, keyFile string) (hostexec.HostClient, error) {
			return &mockHostClient{
				outputs: map[string]string{
					"echo \"$JSON_PAYLOAD\"": "{\n  \"key\": \"value\"\n}",
				},
			}, nil
		}),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: "mockhost"}, 22)

	cueContent := `
recipe: {
	name: "test-multiline-env"
	type: "linear"
	steps: [
		{
			host: "*"
			command: "echo \"$JSON_PAYLOAD\""
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)

	cliEnv := map[string]string{
		"JSON_PAYLOAD": "{\n  \"key\": \"value\"\n}",
	}

	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		SSHUser: "testuser",
		Execute: true,
		CLIEnv:  cliEnv,
		Reg:     reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 1)
	assert.True(t, results[0].Success, "Expected success, got error: %s", results[0].ErrMsg)
	assert.Contains(t, results[0].Output, "{\n  \"key\": \"value\"\n}")
}


func TestRecipeE2E_PutStep(t *testing.T) {
	reg := &testRegistry{
		Dialer: DialerFunc(func(user, host string, port int, keyFile string) (hostexec.HostClient, error) {
			return &mockHostClient{
				outputs: map[string]string{
					"cat /tmp/uploaded_test.txt": "hello from local",
				},
			}, nil
		}),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: "mockhost"}, 22)

	// Create a temporary local file to upload
	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "upload_test.txt")
	err := os.WriteFile(localFile, []byte("hello from local"), 0644)
	require.NoError(t, err)

	cueContent := `
recipe: {
	name: "test-put-step"
	type: "linear"
	steps: [
		{
			host: "*"
			put: {
				local: "` + localFile + `"
				remote: "/tmp/uploaded_test.txt"
			}
		},
		{
			host: "*"
			command: "cat /tmp/uploaded_test.txt"
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)
	
	params := engine.CueRecipeRunParams{
		Recipe:    recipe,
		RecipeDir: tmpDir,
		Records:   []hosts.Record{rec},
		SSHUser:   "testuser",
		Execute:   true,
		Reg:       reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 2)
	assert.True(t, results[0].Success) // Put step success
	assert.True(t, results[1].Success) // Command step success
	assert.Contains(t, results[1].Output, "hello from local")
}
