package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/audit"
)

func writeTestAuditFile(t *testing.T, events []audit.Event) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "audit*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatal(err)
		}
	}
	return f.Name()
}

func TestWriteAuditExport_jsonl(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Source: "mcp", Action: "exec", Target: "web-1", Decision: "allow", Risk: "low"},
		{Time: now, Actor: "bob", Source: "web", Action: "exec", Target: "db-1", Decision: "deny", Risk: "critical"},
	}

	var buf bytes.Buffer
	if err := writeAuditExport(&buf, events, "jsonl"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %s", len(lines), buf.String())
	}
	var e audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("line 0 not valid JSON: %v", err)
	}
	if e.Actor != "alice" {
		t.Errorf("Actor = %q, want alice", e.Actor)
	}
}

func TestWriteAuditExport_table(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Source: "mcp", Action: "exec", Target: "web-1", Decision: "allow", Risk: "low"},
	}

	var buf bytes.Buffer
	if err := writeAuditExport(&buf, events, "table"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ACTOR") {
		t.Errorf("missing header in table output: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("missing actor in table output: %s", out)
	}
}

func TestWriteAuditExport_csv(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Source: "mcp", Action: "exec", Target: "web-1", Decision: "allow", Risk: "low"},
	}

	var buf bytes.Buffer
	if err := writeAuditExport(&buf, events, "csv"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "actor") {
		t.Errorf("missing csv header: %s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("missing alice in csv: %s", out)
	}
}

func TestWriteAuditExport_unknownFormat(t *testing.T) {
	var buf bytes.Buffer
	err := writeAuditExport(&buf, nil, "xml")
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestReadAuditEvents_filterByDecision(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Action: "exec", Decision: "allow"},
		{Time: now, Actor: "bob", Action: "exec", Decision: "deny"},
	}
	path := writeTestAuditFile(t, events)

	got, err := readAuditEvents(path, time.Time{}, "", "", "deny")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].Actor != "bob" {
		t.Errorf("Actor = %q, want bob", got[0].Actor)
	}
}

func TestReadAuditEvents_filterByActor(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Action: "exec", Decision: "allow"},
		{Time: now, Actor: "bob", Action: "exec", Decision: "allow"},
	}
	path := writeTestAuditFile(t, events)

	got, err := readAuditEvents(path, time.Time{}, "alice", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Actor != "alice" {
		t.Errorf("filter by actor failed: %+v", got)
	}
}

func TestReadAuditEvents_since(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	recent := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: old, Actor: "alice", Action: "exec", Decision: "allow"},
		{Time: recent, Actor: "bob", Action: "exec", Decision: "allow"},
	}
	path := writeTestAuditFile(t, events)

	cutoff := time.Now().Add(-1 * time.Hour)
	got, err := readAuditEvents(path, cutoff, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 recent event, got %d", len(got))
	}
	if got[0].Actor != "bob" {
		t.Errorf("Actor = %q, want bob", got[0].Actor)
	}
}

func TestReadAuditEvents_missingFile(t *testing.T) {
	_, err := readAuditEvents("/no/such/file.jsonl", time.Time{}, "", "", "")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestMatchesFilter_zeroSince(t *testing.T) {
	e := audit.Event{Time: time.Now(), Actor: "alice", Action: "exec", Decision: "allow"}
	if !matchesFilter(e, time.Time{}, "", "", "") {
		t.Error("zero since should not filter out events")
	}
}

func TestMatchesFilter_caseInsensitive(t *testing.T) {
	e := audit.Event{Time: time.Now(), Actor: "Alice", Action: "Exec", Decision: "Allow"}
	if !matchesFilter(e, time.Time{}, "alice", "exec", "allow") {
		t.Error("filter should be case-insensitive")
	}
}

func TestParseSince_empty(t *testing.T) {
	ts, err := parseSince("")
	if err != nil {
		t.Fatal(err)
	}
	if !ts.IsZero() {
		t.Error("empty since should return zero time")
	}
}

func TestParseSince_valid(t *testing.T) {
	ts, err := parseSince("1h")
	if err != nil {
		t.Fatal(err)
	}
	if ts.IsZero() {
		t.Error("1h since should return non-zero time")
	}
	if time.Since(ts) < 59*time.Minute || time.Since(ts) > 61*time.Minute {
		t.Errorf("since = %v, expected ~1h ago", ts)
	}
}

func TestParseSince_invalid(t *testing.T) {
	_, err := parseSince("notaduration")
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}
