package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/assert"
)

func TestLocalTransport_Success(t *testing.T) {
	tr := &localTransport{}
	tc := TargetContext{
		Record: hosts.Record{Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"},
	}

	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		return "echo 'hello native execution'"
	}

	res := tr.RunCommand(context.Background(), "", tc, nil, false, cmdFunc, BatchOptions{})
	assert.True(t, res.Success)
	assert.Equal(t, "hello native execution", res.Output)
	assert.Equal(t, 0, res.ExitCode)
}

func TestLocalTransport_NonZeroExit(t *testing.T) {
	tr := &localTransport{}
	tc := TargetContext{
		Record: hosts.Record{Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"},
	}

	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		return "exit 42"
	}

	res := tr.RunCommand(context.Background(), "", tc, nil, false, cmdFunc, BatchOptions{})
	assert.False(t, res.Success)
	assert.Equal(t, 42, res.ExitCode)
	assert.Contains(t, res.ErrMsg, "exit 42")
}

func TestLocalTransport_Timeout(t *testing.T) {
	tr := &localTransport{}
	tc := TargetContext{
		Record: hosts.Record{Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"},
	}

	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		return "sleep 2"
	}

	res := tr.RunCommand(context.Background(), "", tc, nil, false, cmdFunc, BatchOptions{
		CmdTimeout: 10 * time.Millisecond,
	})
	assert.False(t, res.Success)
	assert.Contains(t, res.ErrMsg, "command timed out")
}

func TestLocalTransport_Truncation(t *testing.T) {
	tr := &localTransport{}
	tc := TargetContext{
		Record: hosts.Record{Name: cuetry.MatchLocalAIHost, PrimaryIP: "-"},
	}

	cmdFunc := func(_ TargetContext, _ map[string]string) string {
		// Output 100 bytes
		return "for i in $(seq 1 10); do echo '123456789'; done"
	}

	res := tr.RunCommand(context.Background(), "", tc, nil, false, cmdFunc, BatchOptions{
		MaxOutputBytes: 15,
	})
	assert.True(t, res.Success)
	assert.True(t, strings.HasSuffix(res.Output, "…(truncated)"), "output should be truncated")
	assert.LessOrEqual(t, len(res.Output), 15+len("\n…(truncated)"), "output length should respect the limit")
}
