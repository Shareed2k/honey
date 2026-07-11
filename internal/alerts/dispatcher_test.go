package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/nikoksr/notify"
	"github.com/stretchr/testify/assert"
)

type mockService struct {
	sent int
}

func (m *mockService) Send(_ context.Context, _, _ string) error {
	m.sent++
	return nil
}

func TestDispatcher(t *testing.T) {
	n := notify.New()
	mock := &mockService{}
	n.UseServices(mock)

	// Test with suppression
	d := New(n, 100*time.Millisecond)
	defer d.Close()

	ctx := context.Background()

	// First dispatch should send
	d.Dispatch(ctx, "source1", 0.9, "category:reason", "log line")
	assert.Equal(t, 1, mock.sent)

	// Second dispatch with same fingerprint within window should be suppressed
	d.Dispatch(ctx, "source1", 0.8, "category:other", "log line 2")
	assert.Equal(t, 1, mock.sent)

	// Dispatch with different source should send
	d.Dispatch(ctx, "source2", 0.9, "category:reason", "log line")
	assert.Equal(t, 2, mock.sent)

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Dispatch with same fingerprint after TTL should send
	d.Dispatch(ctx, "source1", 0.9, "category:reason", "log line")
	assert.Equal(t, 3, mock.sent)
}

func TestDispatcher_NoSuppress(t *testing.T) {
	n := notify.New()
	mock := &mockService{}
	n.UseServices(mock)

	// Test without suppression (0 TTL)
	d := New(n, 0)
	defer d.Close()

	ctx := context.Background()

	d.Dispatch(ctx, "source1", 0.9, "category:reason", "log line")
	assert.Equal(t, 1, mock.sent)

	d.Dispatch(ctx, "source1", 0.8, "category:other", "log line 2")
	assert.Equal(t, 2, mock.sent)
}

func TestFingerprint(t *testing.T) {
	fp1 := fingerprint("source1", "cat:reason1")
	fp2 := fingerprint("source1", "cat:reason2")
	fp3 := fingerprint("source1", "other:reason1")
	fp4 := fingerprint("source2", "cat:reason1")

	assert.Equal(t, fp1, fp2)    // Same source and category
	assert.NotEqual(t, fp1, fp3) // Same source, different category
	assert.NotEqual(t, fp1, fp4) // Different source
}
