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
)

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
	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}

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

func (r *SessionRecorder) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

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

func (r *SessionRecorder) RecordError(err error) {
	if r == nil || err == nil {
		return
	}
	r.recordEvent(sessionRecordEvent{
		Type:    "error",
		Message: err.Error(),
	})
}

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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r == nil || r.file == nil {
		return
	}
	evt.TimeMS = time.Since(r.start).Milliseconds()
	_ = r.enc.Encode(evt)
}

type recordingReader struct {
	inner     io.Reader
	recorder  *SessionRecorder
	direction string
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

func WrapRecordingReader(inner io.Reader, recorder *SessionRecorder, direction string) io.Reader {
	if recorder == nil || inner == nil {
		return inner
	}
	return &recordingReader{inner: inner, recorder: recorder, direction: direction}
}

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
