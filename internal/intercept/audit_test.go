package intercept

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/audit"
)

// recordingSink captures every audit event for assertions.
type recordingSink struct {
	events []audit.Event
}

func (s *recordingSink) Log(_ context.Context, e audit.Event) error {
	s.events = append(s.events, e)
	return nil
}

func (s *recordingSink) Close() error { return nil }

func sampleEvent() Event {
	return Event{
		Actor:      "roman",
		Cluster:    "prod",
		Namespace:  "apps",
		Pod:        "api-0",
		Container:  "app",
		Mode:       []string{"egress", "files"},
		AgentImage: "registry.example/agent:1",
	}
}

// assertNoToken guards the never-log-a-token invariant: no key or value in the
// event carries anything resembling a token.
func assertNoToken(t *testing.T, e audit.Event) {
	t.Helper()
	assert.Empty(t, e.ApprovalID)
	for k, v := range e.Extra {
		assert.NotContains(t, k, "token", "audit extra must not carry a token key")
		assert.NotContains(t, v, "token", "audit extra value must not mention a token")
	}
}

func TestAuditStart(t *testing.T) {
	t.Parallel()

	sink := &recordingSink{}
	auditStart(context.Background(), sink, sampleEvent())

	require.Len(t, sink.events, 1)
	e := sink.events[0]
	assert.Equal(t, "intercept_start", e.Action)
	assert.Equal(t, "cli", e.Source)
	assert.Equal(t, "roman", e.Actor)
	assert.Equal(t, "prod", e.Target)
	assert.Equal(t, "apps", e.Extra["namespace"])
	assert.Equal(t, "api-0", e.Extra["pod"])
	assert.Equal(t, "app", e.Extra["container"])
	assert.Equal(t, "egress,files", e.Extra["mode"])
	assert.Equal(t, "registry.example/agent:1", e.Extra["image"])
	// Start events carry no outcome fields.
	assert.NotContains(t, e.Extra, "duration")
	assert.NotContains(t, e.Extra, "reason")
	assertNoToken(t, e)
}

func TestAuditStop(t *testing.T) {
	t.Parallel()

	ev := sampleEvent()
	ev.Duration = 90 * time.Second
	ev.Reason = "context canceled"

	sink := &recordingSink{}
	auditStop(context.Background(), sink, ev)

	require.Len(t, sink.events, 1)
	e := sink.events[0]
	assert.Equal(t, "intercept_stop", e.Action)
	assert.Equal(t, "cli", e.Source)
	assert.Equal(t, "1m30s", e.Extra["duration"])
	assert.Equal(t, "context canceled", e.Extra["reason"])
	assertNoToken(t, e)
}

func TestAudit_nilSinkNoPanic(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		auditStart(context.Background(), nil, sampleEvent())
		auditStop(context.Background(), nil, sampleEvent())
	})
}
