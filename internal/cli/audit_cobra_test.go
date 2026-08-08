package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
)

// withAuditConfig sets resolvedCfg to a config whose audit path points at path,
// and restores the original value after the test.
func withAuditConfig(t *testing.T, path string) {
	t.Helper()
	prev := resolvedCfg
	cfg := &config.File{}
	cfg.Audit.Path = path
	cfg.Audit.Enabled = true
	resolvedCfg = cfg
	t.Cleanup(func() { resolvedCfg = prev })
}

// withExportFlags sets the audit export flag vars and restores them after the test.
func withExportFlags(t *testing.T, format, since, actor, action, decision string) {
	t.Helper()
	prevFmt := auditExportFormat
	prevSince := auditExportSince
	prevActor := auditExportActor
	prevAction := auditExportAction
	prevDec := auditExportDec
	auditExportFormat = format
	auditExportSince = since
	auditExportActor = actor
	auditExportAction = action
	auditExportDec = decision
	t.Cleanup(func() {
		auditExportFormat = prevFmt
		auditExportSince = prevSince
		auditExportActor = prevActor
		auditExportAction = prevAction
		auditExportDec = prevDec
	})
}

// execAuditExport calls runAuditExport with a captured stdout buffer.
func execAuditExport(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := runAuditExport(cmd, nil); err != nil {
		t.Fatalf("runAuditExport: %v", err)
	}
	return buf.String()
}

func TestAuditCobra_Export_jsonl(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Source: "mcp", Action: "exec", Target: "web-1", Decision: "allow", Risk: "low"},
		{Time: now, Actor: "bob", Source: "mcp", Action: "exec", Target: "db-1", Decision: "deny", Risk: "critical"},
	}
	path := writeTestAuditFile(t, events)
	withAuditConfig(t, path)
	withExportFlags(t, "jsonl", "", "", "", "")

	out := execAuditExport(t)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d:\n%s", len(lines), out)
	}
	var e audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("line 0 not valid JSON: %v", err)
	}
	if e.Actor != "alice" {
		t.Errorf("Actor = %q, want alice", e.Actor)
	}
}

func TestAuditCobra_Export_table(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Source: "mcp", Action: "exec", Target: "web-1", Decision: "allow", Risk: "low"},
	}
	path := writeTestAuditFile(t, events)
	withAuditConfig(t, path)
	withExportFlags(t, "table", "", "", "", "")

	out := execAuditExport(t)

	if !strings.Contains(out, "ACTOR") {
		t.Errorf("missing header in table output:\n%s", out)
	}
	if !strings.Contains(out, "alice") {
		t.Errorf("missing alice in table output:\n%s", out)
	}
}

func TestAuditCobra_Export_filterDecision(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Action: "exec", Decision: "allow"},
		{Time: now, Actor: "bob", Action: "exec", Decision: "deny"},
	}
	path := writeTestAuditFile(t, events)
	withAuditConfig(t, path)
	withExportFlags(t, "jsonl", "", "", "", "deny")

	out := execAuditExport(t)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after deny filter, got %d:\n%s", len(lines), out)
	}
	var e audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Actor != "bob" {
		t.Errorf("Actor = %q, want bob", e.Actor)
	}
}

func TestAuditCobra_Export_filterSince(t *testing.T) {
	old := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	recent := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: old, Actor: "alice", Action: "exec", Decision: "allow"},
		{Time: recent, Actor: "bob", Action: "exec", Decision: "allow"},
	}
	path := writeTestAuditFile(t, events)
	withAuditConfig(t, path)
	withExportFlags(t, "jsonl", "1h", "", "", "")

	out := execAuditExport(t)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 recent event after since=1h filter, got %d:\n%s", len(lines), out)
	}
	var e audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Actor != "bob" {
		t.Errorf("Actor = %q, want bob", e.Actor)
	}
}

func TestAuditCobra_Export_missingFile(t *testing.T) {
	withAuditConfig(t, "/no/such/path/audit.jsonl")
	withExportFlags(t, "jsonl", "", "", "", "")

	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	// Audit is opt-in: a missing log is explained (hint on stderr), not an error.
	if err := runAuditExport(cmd, nil); err != nil {
		t.Fatalf("missing audit file should not error, got %v", err)
	}
	if !strings.Contains(errBuf.String(), "no audit log") {
		t.Errorf("expected a missing-log hint on stderr, got %q", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("expected no stdout output, got %q", out.String())
	}
}

func TestAuditCobra_Export_filterActor(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Action: "exec", Decision: "allow"},
		{Time: now, Actor: "bob", Action: "exec", Decision: "allow"},
	}
	path := writeTestAuditFile(t, events)
	withAuditConfig(t, path)
	withExportFlags(t, "jsonl", "", "alice", "", "")

	out := execAuditExport(t)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after actor=alice filter, got %d:\n%s", len(lines), out)
	}
	var e audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Actor != "alice" {
		t.Errorf("Actor = %q, want alice", e.Actor)
	}
}

func TestAuditCobra_Export_filterAction(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	events := []audit.Event{
		{Time: now, Actor: "alice", Action: "exec", Decision: "allow"},
		{Time: now, Actor: "alice", Action: "recipe_run", Decision: "allow"},
	}
	path := writeTestAuditFile(t, events)
	withAuditConfig(t, path)
	withExportFlags(t, "jsonl", "", "", "recipe_run", "")

	out := execAuditExport(t)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line after action=recipe_run filter, got %d:\n%s", len(lines), out)
	}
	var e audit.Event
	if err := json.Unmarshal([]byte(lines[0]), &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Action != "recipe_run" {
		t.Errorf("Action = %q, want recipe_run", e.Action)
	}
}

func TestAuditCobra_Export_csv(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f, err := os.CreateTemp(t.TempDir(), "audit*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	_ = enc.Encode(audit.Event{Time: now, Actor: "carol", Action: "exec", Decision: "allow", Risk: "low"})
	f.Close()

	withAuditConfig(t, f.Name())
	withExportFlags(t, "csv", "", "", "", "")

	out := execAuditExport(t)

	if !strings.Contains(out, "actor") {
		t.Errorf("missing csv header:\n%s", out)
	}
	if !strings.Contains(out, "carol") {
		t.Errorf("missing carol in csv:\n%s", out)
	}
}
