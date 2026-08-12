package intercept

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMintToken(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{})
	for range 8 {
		tok, err := mintToken()
		require.NoError(t, err)

		raw, err := hex.DecodeString(tok)
		require.NoError(t, err, "token must be valid hex")
		assert.GreaterOrEqual(t, len(raw), 32, "token must decode to >= 32 bytes")

		_, dup := seen[tok]
		assert.False(t, dup, "tokens must be unique")
		seen[tok] = struct{}{}
	}
}

func TestWriteTokenFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tok := "deadbeefcafe"

	p, err := writeTokenFile(dir, tok)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "token"), p)

	info, err := os.Stat(p)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "token file must be mode 0600")

	data, err := os.ReadFile(p)
	require.NoError(t, err)
	assert.Equal(t, tok, string(data))
}

// fakeExecer records the argv and stdin of the ExecInPod call so tests can
// assert what deliverToken sent, and can inject a failure.
type fakeExecer struct {
	gotCmd   []string
	gotStdin string
	err      error
	stderr   string
}

func (f *fakeExecer) ExecInPod(_ context.Context, cmd []string, stdin io.Reader, _, stderr io.Writer) error {
	f.gotCmd = cmd
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		f.gotStdin = string(b)
	}
	if f.stderr != "" && stderr != nil {
		_, _ = stderr.Write([]byte(f.stderr))
	}
	return f.err
}

func TestDeliverToken(t *testing.T) {
	t.Parallel()

	const token = "intercept-test-token-not-a-real-secret"
	fe := &fakeExecer{}

	err := deliverToken(context.Background(), fe, token)
	require.NoError(t, err)

	// The token travels via stdin, never via argv.
	require.NotEmpty(t, fe.gotCmd)
	joined := strings.Join(fe.gotCmd, " ")
	assert.NotContains(t, joined, token, "token must never appear on the command line")
	assert.Contains(t, joined, "/var/run/mogate/token", "command must write the agent token path")
	assert.Contains(t, joined, "cat >", "command must read the token from stdin")

	assert.Equal(t, token, fe.gotStdin, "token must be streamed to stdin")
}

func TestDeliverToken_errorHidesToken(t *testing.T) {
	t.Parallel()

	const token = "aaaabbbbccccddddeeeeffff00001111aaaabbbbccccddddeeeeffff00001111"
	fe := &fakeExecer{err: errors.New("exec: boom"), stderr: "permission denied"}

	err := deliverToken(context.Background(), fe, token)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), token, "returned error must not leak the token")
	assert.Contains(t, err.Error(), "permission denied")
}
