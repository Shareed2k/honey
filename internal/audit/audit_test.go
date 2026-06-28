package audit_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/audit"
)

func TestNoopSink_neverErrors(t *testing.T) {
	t.Parallel()
	s := audit.NewNoopSink()
	err := s.Log(context.Background(), audit.Event{
		Time:   time.Now(),
		Actor:  "test",
		Action: "exec",
	})
	require.NoError(t, err)
	require.NoError(t, s.Close())
}

func TestFileSink_writesValidJSONL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	s, err := audit.NewFileSink(path)
	require.NoError(t, err)

	evt := audit.Event{
		Time:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Actor:    "roman",
		Source:   "mcp",
		Action:   "exec",
		Target:   "prod-1",
		Command:  "whoami",
		Risk:     "low",
		Decision: "allow",
	}
	require.NoError(t, s.Log(context.Background(), evt))
	require.NoError(t, s.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var got audit.Event
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "roman", got.Actor)
	assert.Equal(t, "exec", got.Action)
	assert.Equal(t, "prod-1", got.Target)
	assert.Equal(t, "allow", got.Decision)
}

func TestFileSink_appendsMultipleEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	s, err := audit.NewFileSink(path)
	require.NoError(t, err)
	defer s.Close()

	for i := 0; i < 3; i++ {
		require.NoError(t, s.Log(context.Background(), audit.Event{
			Action: "exec",
			Actor:  "user",
		}))
	}
	require.NoError(t, s.Close())

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var lines int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if sc.Text() != "" {
			lines++
		}
	}
	assert.Equal(t, 3, lines)
}

func TestFileSink_concurrentWritesDontInterleave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	s, err := audit.NewFileSink(path)
	require.NoError(t, err)
	defer s.Close()

	const goroutines = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Log(context.Background(), audit.Event{Action: "exec", Actor: "u"})
		}()
	}
	wg.Wait()
	require.NoError(t, s.Close())

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var count int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		var e audit.Event
		require.NoError(t, json.Unmarshal([]byte(line), &e), "line not valid JSON: %s", line)
		count++
	}
	assert.Equal(t, goroutines, count)
}

func TestFileSink_roundtripDenyReason(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	s, err := audit.NewFileSink(path)
	require.NoError(t, err)

	exitCode := 1
	require.NoError(t, s.Log(context.Background(), audit.Event{
		Action:     "exec",
		Decision:   "deny",
		DenyReason: "command risk: critical",
		ExitCode:   &exitCode,
	}))
	require.NoError(t, s.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var got audit.Event
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "deny", got.Decision)
	assert.Equal(t, "command risk: critical", got.DenyReason)
	require.NotNil(t, got.ExitCode)
	assert.Equal(t, 1, *got.ExitCode)
}
