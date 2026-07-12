package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptRunner_RunLocalHost_Success(t *testing.T) {
	tmpDir := t.TempDir()
	localScript := filepath.Join(tmpDir, "test.sh")
	require.NoError(t, os.WriteFile(localScript, []byte("echo 'local script ran'"), 0600)) // runner will chmod +x

	// Remote path is what the script command was built with (e.g. SFTP dest)
	// But it shouldn't matter since runLocalHost replaces it with localAbs.
	remotePath := "/tmp/honey-remote.sh"
	
	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		return "sh -c " + remotePath
	}

	opts := ScriptUploadRunOptions{}
	runner, err := newScriptRunner("user", localScript, remotePath, false, cmdFunc, opts, nil, nil, false, 6000)
	require.NoError(t, err)

	tc := TargetContext{
		Record: hosts.Record{Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"},
	}

	res := runner.runLocalHost(context.Background(), tc)
	
	assert.True(t, res.Success)
	assert.Equal(t, 0, res.ExitCode)
	assert.Equal(t, "local script ran", res.Output)
	
	// verify the script became executable
	info, err := os.Stat(localScript)
	require.NoError(t, err)
	assert.True(t, info.Mode()&0111 != 0, "script should be executable")
}

func TestScriptRunner_RunLocalHost_Timeout(t *testing.T) {
	tmpDir := t.TempDir()
	localScript := filepath.Join(tmpDir, "test.sh")
	require.NoError(t, os.WriteFile(localScript, []byte("sleep 2"), 0600))

	remotePath := "/tmp/honey-remote.sh"
	
	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		return remotePath
	}

	opts := ScriptUploadRunOptions{}
	runner, err := newScriptRunner("user", localScript, remotePath, false, cmdFunc, opts, nil, nil, false, 6000)
	require.NoError(t, err)
	runner.cmdTimeout = 10 * time.Millisecond

	tc := TargetContext{
		Record: hosts.Record{Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"},
	}

	res := runner.runLocalHost(context.Background(), tc)
	
	assert.False(t, res.Success)
	assert.Contains(t, res.ErrMsg, "command timed out after")
}

func TestScriptRunner_Stream_DispatchesLocal(t *testing.T) {
	tmpDir := t.TempDir()
	localScript := filepath.Join(tmpDir, "test.sh")
	require.NoError(t, os.WriteFile(localScript, []byte("echo 'local'"), 0600))

	remotePath := "/tmp/honey-remote.sh"
	cmdFunc := func(_ TargetContext, _ map[string]string) string { return remotePath }
	
	runner, err := newScriptRunner("user", localScript, remotePath, false, cmdFunc, ScriptUploadRunOptions{}, nil, nil, false, 6000)
	require.NoError(t, err)

	tc := TargetContext{
		Record: hosts.Record{Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"},
	}
	
	outCh := make(chan HostExecResult, 1)
	var maxAttempts atomic.Int32
	runner.stream(context.Background(), []TargetContext{tc}, 1, outCh, nil, cuetry.RecipeStepRetry{}, nil, &maxAttempts)
	
	res := <-outCh
	assert.True(t, res.Success)
	assert.Equal(t, "local", strings.TrimSpace(res.Output))
}
