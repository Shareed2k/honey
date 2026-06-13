package ui

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// SessionRecorderOptions configures filename segments and metadata for a new session recording.
type SessionRecorderOptions struct {
	Dir      string
	Trigger  string
	Mode     string
	Provider string
	HostName string
	HostIP   string
	User     string
	// HostSegment, when set, is used for the filename segment instead of HostName/IP (e.g. batch-12).
	HostSegment string
}

// SessionRecorder appends JSONL events to a single .hrec.jsonl file (TTY data, resize, errors, close).
type SessionRecorder struct {
	mu    sync.Mutex
	file  *os.File
	enc   *json.Encoder
	path  string
	start time.Time
}

type sessionRecordEvent struct {
	TimeMS    int64           `json:"time_ms"`
	Type      string          `json:"type"`
	Direction string          `json:"direction,omitempty"`
	DataB64   string          `json:"data_b64,omitempty"`
	Cols      int             `json:"cols,omitempty"`
	Rows      int             `json:"rows,omitempty"`
	Message   string          `json:"message,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// NewSessionRecorder creates a recorder writing to opts.Dir with a timestamped filename.
func NewSessionRecorder(opts SessionRecorderOptions) (*SessionRecorder, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		return nil, fmt.Errorf("empty recorder dir")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}

	safeHost := sanitizeRecorderPart(opts.HostSegment)
	if safeHost == "" {
		safeHost = sanitizeRecorderPart(opts.HostName)
	}
	if safeHost == "" {
		safeHost = sanitizeRecorderPart(opts.HostIP)
	}
	if safeHost == "" {
		safeHost = "unknown-host"
	}
	mode := sanitizeRecorderPart(opts.Mode)
	if mode == "" {
		mode = "unknown-mode"
	}
	trigger := sanitizeRecorderPart(opts.Trigger)
	if trigger == "" {
		trigger = "unknown-trigger"
	}
	provider := sanitizeRecorderPart(opts.Provider)
	if provider == "" {
		provider = "unknown-provider"
	}

	fileName := fmt.Sprintf(
		"%s_%s_%s_%s_%s.hrec.jsonl",
		time.Now().Format("20060102_150405"),
		trigger,
		mode,
		provider,
		safeHost,
	)
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	f, err := root.OpenFile(fileName, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, fileName)

	r := &SessionRecorder{
		file:  f,
		enc:   json.NewEncoder(f),
		path:  path,
		start: time.Now(),
	}
	r.recordEvent(sessionRecordEvent{
		Type:    "open",
		Message: fmt.Sprintf("trigger=%s mode=%s provider=%s host=%s ip=%s user=%s", opts.Trigger, opts.Mode, opts.Provider, opts.HostName, opts.HostIP, opts.User),
	})
	return r, nil
}

// NewBatchSessionRecorder creates a recorder for one parallel exec or CUE batch run (one file per invocation).
func NewBatchSessionRecorder(dir, trigger, user string, jobCount int) (*SessionRecorder, error) {
	hostLabel := "batch-0"
	if jobCount > 0 {
		hostLabel = fmt.Sprintf("batch-%d", jobCount)
	}
	return NewSessionRecorder(SessionRecorderOptions{
		Dir:         dir,
		Trigger:     trigger,
		Mode:        "batch",
		Provider:    "mixed",
		HostName:    hostLabel,
		HostIP:      "",
		User:        user,
		HostSegment: hostLabel,
	})
}

// Path returns the absolute path of the recording file, or empty if r is nil.
func (r *SessionRecorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// RecordHostExecResult writes one structured "result" event (parallel exec / batch output).
func (r *SessionRecorder) RecordHostExecResult(res HostExecResult) {
	if r == nil {
		return
	}
	b, err := json.Marshal(res)
	if err != nil {
		return
	}
	r.recordEvent(sessionRecordEvent{
		Type:   "result",
		Result: json.RawMessage(b),
	})
}

// RecordData writes a base64-encoded payload for the given direction (e.g. in/out).
func (r *SessionRecorder) RecordData(direction string, payload []byte) {
	if r == nil || len(payload) == 0 {
		return
	}
	r.recordEvent(sessionRecordEvent{
		Type:      "data",
		Direction: direction,
		DataB64:   base64.StdEncoding.EncodeToString(payload),
	})
}

// RecordResize records a terminal resize event.
func (r *SessionRecorder) RecordResize(cols, rows int) {
	if r == nil {
		return
	}
	r.recordEvent(sessionRecordEvent{
		Type: "resize",
		Cols: cols,
		Rows: rows,
	})
}

// RecordError records a non-fatal error message on the session.
func (r *SessionRecorder) RecordError(err error) {
	if r == nil || err == nil {
		return
	}
	r.recordEvent(sessionRecordEvent{
		Type:    "error",
		Message: err.Error(),
	})
}

// RecipeMeta describes the recipe that a cue-exec batch is about to run.
// Recorded into the session file so a later "recent runs" enumeration can
// attribute the recording to a recipe and detect in-browser edits.
type RecipeMeta struct {
	RecipePath        string                  `json:"recipe_path"`
	HostCount         int                     `json:"host_count"`
	RecipeContentHash string                  `json:"recipe_content_hash"`
	StartedAt         time.Time               `json:"started_at"`
	Hosts             []hosts.Record          `json:"hosts,omitempty"`
	Plan              string                  `json:"plan,omitempty"`
	Graph             *cuetry.RecipeGraphPlan `json:"graph,omitempty"`
}

// HostsForRecipeMeta copies up to limit connectable host records for recipe-meta (web re-run).
func HostsForRecipeMeta(jobs []hosts.Record, limit int) []hosts.Record {
	if limit <= 0 || len(jobs) == 0 {
		return nil
	}
	n := len(jobs)
	if n > limit {
		n = limit
	}
	out := make([]hosts.Record, 0, n)
	for i := 0; i < n; i++ {
		r := jobs[i]
		out = append(out, hosts.Record{
			Provider:  r.Provider,
			Name:      r.Name,
			PrimaryIP: r.PrimaryIP,
			ExtraIPs:  r.ExtraIPs,
			Meta:      r.Meta,
		})
	}
	return out
}

// RecordingFileBase returns the recording filename (e.g. 20260102_120000_web-cue-exec_batch_mixed_batch-3.hrec.jsonl).
func (r *SessionRecorder) RecordingFileBase() string {
	if r == nil {
		return ""
	}
	return filepath.Base(r.path)
}

// RecordingID returns the recording id (filename without .hrec.jsonl).
func (r *SessionRecorder) RecordingID() string {
	base := r.RecordingFileBase()
	return strings.TrimSuffix(base, ".hrec.jsonl")
}

// RecordRecipeMeta writes one "recipe-meta" structured event into the recording.
// Safe to call on a nil recorder.
func (r *SessionRecorder) RecordRecipeMeta(meta RecipeMeta) {
	if r == nil {
		return
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return
	}
	r.recordEvent(sessionRecordEvent{
		Type:   "recipe-meta",
		Result: json.RawMessage(b),
	})
}

// Close writes a "close" event and closes the underlying file.
func (r *SessionRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.recordEvent(sessionRecordEvent{Type: "close"})
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	return err
}

func (r *SessionRecorder) recordEvent(evt sessionRecordEvent) {
	if r == nil || r.file == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	evt.TimeMS = time.Since(r.start).Milliseconds()
	_ = r.enc.Encode(evt)
}

type recordingReader struct {
	inner     io.Reader
	recorder  *SessionRecorder
	direction string
}

// SetReadDeadline forwards read deadlines to the underlying reader when supported (e.g. *os.File),
// so interactive pumps can unblock stdin after the remote session ends.
func (r *recordingReader) SetReadDeadline(t time.Time) error {
	if r == nil {
		return fmt.Errorf("nil recording reader")
	}
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}
	if d, ok := r.inner.(deadliner); ok {
		return d.SetReadDeadline(t)
	}
	return nil
}

func (r *recordingReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.recorder.RecordData(r.direction, p[:n])
	}
	return n, err
}

type recordingWriter struct {
	inner     io.Writer
	recorder  *SessionRecorder
	direction string
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	if n > 0 {
		w.recorder.RecordData(w.direction, p[:n])
	}
	return n, err
}

// WrapRecordingReader returns a Reader that tees reads into recorder when non-nil.
func WrapRecordingReader(inner io.Reader, recorder *SessionRecorder, direction string) io.Reader {
	if recorder == nil || inner == nil {
		return inner
	}
	return &recordingReader{inner: inner, recorder: recorder, direction: direction}
}

// WrapRecordingWriter returns a Writer that tees writes into recorder when non-nil.
func WrapRecordingWriter(inner io.Writer, recorder *SessionRecorder, direction string) io.Writer {
	if recorder == nil || inner == nil {
		return inner
	}
	return &recordingWriter{inner: inner, recorder: recorder, direction: direction}
}

func sanitizeRecorderPart(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, ch := range s {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' {
			b.WriteRune(ch)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
