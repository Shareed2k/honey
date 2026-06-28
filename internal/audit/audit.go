// Package audit provides a durable, append-only audit event log. Events are
// written as newline-delimited JSON (JSONL) so they can be streamed with
// `tail -f`, parsed by any JSON tool, or forwarded to a SIEM. Each event is
// one security-relevant decision: an exec, a recipe run, an approval.
package audit

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one durable record of a security-relevant action. Zero-value
// fields are omitted from JSON so the log stays compact.
type Event struct {
	Time       time.Time         `json:"time"`
	Actor      string            `json:"actor,omitempty"`
	Source     string            `json:"source,omitempty"` // "mcp"|"web"|"cli"|"webhook"
	Action     string            `json:"action,omitempty"` // "exec"|"recipe_run"|"approval"
	Target     string            `json:"target,omitempty"`
	Command    string            `json:"command,omitempty"`
	Risk       string            `json:"risk,omitempty"`     // "low"|"medium"|"high"|"critical"
	Decision   string            `json:"decision,omitempty"` // "allow"|"deny"|"require_approval"
	DenyReason string            `json:"deny_reason,omitempty"`
	ApprovalID string            `json:"approval_id,omitempty"`
	ExitCode   *int              `json:"exit_code,omitempty"`
	Extra      map[string]string `json:"extra,omitempty"`
}

// Sink receives audit events. Implementations must be safe for concurrent use.
type Sink interface {
	Log(ctx context.Context, e Event) error
	Close() error
}

// noopSink discards all events. Used when audit is disabled.
type noopSink struct{}

// NewNoopSink returns a Sink that silently discards every event.
func NewNoopSink() Sink { return noopSink{} }

func (noopSink) Log(_ context.Context, _ Event) error { return nil }
func (noopSink) Close() error                         { return nil }

// FileSink writes events as JSONL to an append-only file. Each Log call
// writes exactly one newline-terminated JSON object. Concurrent calls are
// serialised by a mutex so lines never interleave.
type FileSink struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// NewFileSink opens (or creates) path for append-only writing and returns a
// FileSink. The caller must call Close when done.
func NewFileSink(path string) (*FileSink, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return &FileSink{f: f, enc: enc}, nil
}

// Log serialises e as a JSON line and appends it to the sink file.
func (s *FileSink) Log(_ context.Context, e Event) error {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enc.Encode(e)
}

// Close flushes and closes the underlying file.
func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.f.Close()
}
