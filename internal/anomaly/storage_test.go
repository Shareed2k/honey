package anomaly

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/jsonutil"
)

type mockStorage struct {
	written []StorageRecord
	closed  bool
}

func (m *mockStorage) Write(_ context.Context, rec StorageRecord) error {
	m.written = append(m.written, rec)
	return nil
}

func (m *mockStorage) WriteBatch(_ context.Context, records []StorageRecord) error {
	m.written = append(m.written, records...)
	return nil
}

func (m *mockStorage) Close() error {
	m.closed = true
	return nil
}

func TestBatchStorageDeduplicationAndBatching(t *testing.T) {
	mock := &mockStorage{}
	// Batch size of 3, timeout of 50ms
	batch := NewBatchStorage(mock, 3, 50*time.Millisecond)

	ctx := context.Background()

	// Write 1: unique postgres normal log
	err := batch.Write(ctx, StorageRecord{
		Timestamp: "2026-06-03T12:00:00Z",
		Source:    "postgres",
		Line:      "2026-06-03 12:00:00 UTC [123] info: statement: select * from users where id = 100",
		Score:     0.0,
		Reason:    "select query",
		Anomaly:   false,
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Write 2: duplicate of write 1 (differing in timestamp, Process ID, and parameter ID) -> should be deduplicated out
	err = batch.Write(ctx, StorageRecord{
		Timestamp: "2026-06-03T12:05:12Z",
		Source:    "postgres",
		Line:      "2026-06-03 12:05:12 UTC [129] info: statement: select * from users where id = 500",
		Score:     0.0,
		Reason:    "select query",
		Anomaly:   false,
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Write 3: unique postgres error log
	err = batch.Write(ctx, StorageRecord{
		Timestamp: "2026-06-03T12:00:02Z",
		Source:    "postgres",
		Line:      "2026-06-03 12:00:02 UTC [129] error: duplicate key violates constraint",
		Score:     0.92,
		Reason:    "duplicate key",
		Anomaly:   true,
	})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// Close flushes remaining records and blocks until the worker goroutine exits.
	// Reading mock.written after this is race-free: all writes happen-before wg.Wait().
	if err := batch.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if len(mock.written) != 2 {
		t.Errorf("expected 2 unique records written to mock storage, got %d", len(mock.written))
	}

	// Check that the duplicate was filtered out
	if !strings.Contains(mock.written[0].Line, "select * from users where id = 100") {
		t.Errorf("unexpected first log line: %q", mock.written[0].Line)
	}
	if !strings.Contains(mock.written[1].Line, "error: duplicate key violates constraint") {
		t.Errorf("unexpected second log line: %q", mock.written[1].Line)
	}
}

func TestUDPStorageIntegration(t *testing.T) {
	// Start a local UDP listener
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to resolve: %v", err)
	}
	listener, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	udpAddr := listener.LocalAddr().String()

	udpStore, err := NewUDPStorage(udpAddr)
	if err != nil {
		t.Fatalf("failed to create UDP storage: %v", err)
	}
	defer udpStore.Close()

	rec := StorageRecord{
		Timestamp: "2026-06-03T12:00:00Z",
		Source:    "nginx",
		Line:      "127.0.0.1 - - [03/Jun/2026:12:00:00] GET /index.html 200",
		Score:     0.0,
		Reason:    "web request",
		Anomaly:   false,
	}

	// Write single record over UDP
	ctx := context.Background()
	if err := udpStore.Write(ctx, rec); err != nil {
		t.Fatalf("failed to write over UDP: %v", err)
	}

	// Read from local UDP listener to verify correctness
	buf := make([]byte, 2048)
	_ = listener.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, _, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("failed to read from UDP listener: %v", err)
	}

	packetData := strings.TrimSpace(string(buf[:n]))
	var readRec StorageRecord
	if err := jsonutil.Unmarshal([]byte(packetData), &readRec); err != nil {
		t.Fatalf("failed to unmarshal UDP packet data: %v", err)
	}

	if readRec.Line != rec.Line {
		t.Errorf("expected line %q, got %q", rec.Line, readRec.Line)
	}
}
