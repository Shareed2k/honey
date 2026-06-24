package ui

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/shareed2k/honey/internal/anomaly"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestLogPrefixWithLabels(t *testing.T) {
	rec := hosts.Record{
		Provider: "k8s",
		Name:     "pod-123",
		Meta: map[string]string{
			"backend_name":    "prod",
			"label_env":       "stg",
			"annotation_team": "data",
		},
	}
	got := logPrefix(rec, []string{"env", "team", "missing"})
	want := "[k8s/prod/pod-123 | env=stg team=data] "
	if got != want {
		t.Fatalf("logPrefix = %q, want %q", got, want)
	}
}

func TestWritePrefixedLineFiltering(t *testing.T) {
	var out bytes.Buffer
	var mu sync.Mutex
	re := regexp.MustCompile("(?i)error")

	sink := logSink{out: &out, mu: &mu, prefix: "P: ", grepRe: re}
	// Match
	writePrefixedLine(context.Background(), sink, "An error occurred")
	// No match
	writePrefixedLine(context.Background(), sink, "Just some info")

	got := out.String()
	want := "P: An error occurred\n"
	if got != want {
		t.Fatalf("filtering got %q, want %q", got, want)
	}
}

func TestWritePrefixedLineAnomalyOnly(t *testing.T) {
	var out bytes.Buffer
	var mu sync.Mutex
	d, err := anomaly.NewEmbeddedDetector(anomaly.Options{Threshold: 0.9, Window: 16})
	if err != nil {
		t.Fatalf("new detector: %v", err)
	}

	sink := logSink{out: &out, mu: &mu, prefix: "P: ", detector: d, anomalyOnly: true}
	writePrefixedLine(context.Background(), sink, "all good")
	writePrefixedLine(context.Background(), sink, "panic in worker")

	got := out.String()
	if !strings.Contains(got, "[ANOM score=") {
		t.Fatalf("expected anomaly annotation, got %q", got)
	}
	if strings.Contains(got, "all good") {
		t.Fatalf("expected non-anomaly line filtered, got %q", got)
	}
}

func TestHighlightLogLine(t *testing.T) {
	line := "ERROR: something failed"
	got := highlightLogLine(line)
	if !strings.Contains(got, "something failed") {
		t.Fatalf("highlight lost content: %q", got)
	}
	if got == line {
		t.Fatal("expected line to be highlighted")
	}

	line2 := "Just a normal line"
	got2 := highlightLogLine(line2)
	if got2 != line2 {
		t.Fatalf("should not highlight normal line: %q", got2)
	}
}

func TestLooksLikeLogFileSource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "absolute path", source: "/var/log/app.log", want: true},
		{name: "home path", source: "~/logs/app.log", want: true},
		{name: "relative glob", source: "logs/*.log", want: true},
		{name: "systemd unit", source: "nginx.service", want: false},
		{name: "empty", source: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeLogFileSource(tt.source); got != tt.want {
				t.Fatalf("looksLikeLogFileSource(%q) = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}

func TestLogCommandFileSource(t *testing.T) {
	got := logCommand(LogOptions{Source: "/var/log/app.log", Tail: 25})
	want := "tail -n 25 -- '/var/log/app.log'"
	if got != want {
		t.Fatalf("logCommand file = %q, want %q", got, want)
	}
}

func TestLogCommandGlobFollowSource(t *testing.T) {
	got := logCommand(LogOptions{Source: "/var/log/app/*.log", Follow: true, Tail: 50})
	want := "tail -n 50 -F -- /var/log/app/*.log"
	if got != want {
		t.Fatalf("logCommand glob follow = %q, want %q", got, want)
	}
}

func TestLogCommandBareSourceIsSystemdUnit(t *testing.T) {
	got := logCommand(LogOptions{Target: "prod-api", Source: "nginx.service", Tail: 10})
	want := "journalctl -u 'nginx.service' -n 10 --no-pager -o cat"
	if got != want {
		t.Fatalf("logCommand unit = %q, want %q", got, want)
	}
}

func TestLogCommandDefaultsToTargetUnit(t *testing.T) {
	got := logCommand(LogOptions{Target: "prod-api", Tail: 20})
	want := "journalctl -u 'prod-api' -n 20 --no-pager -o cat"
	if got != want {
		t.Fatalf("logCommand default target = %q, want %q", got, want)
	}
}

func TestLogCommandCustomCommand(t *testing.T) {
	got := logCommand(LogOptions{Command: "journalctl -u custom -f", Tail: 20})
	want := "journalctl -u custom -f"
	if got != want {
		t.Fatalf("logCommand custom = %q, want %q", got, want)
	}
}

func TestLogCommandWithRunAsWrapsGeneratedCommand(t *testing.T) {
	got, err := logCommandWithRunAs(LogOptions{Source: "/var/log/postgresql/server.log", Tail: 100, RunAs: "postgres"})
	if err != nil {
		t.Fatalf("logCommandWithRunAs: %v", err)
	}
	want := `sudo -n -u 'postgres' -- sh -lc 'tail -n 100 -- '\''/var/log/postgresql/server.log'\'''`
	if got != want {
		t.Fatalf("logCommandWithRunAs = %q, want %q", got, want)
	}
}

func TestLogCommandWithRunAsWrapsCustomCommand(t *testing.T) {
	got, err := logCommandWithRunAs(LogOptions{Command: "journalctl -u custom -f", RunAs: "root"})
	if err != nil {
		t.Fatalf("logCommandWithRunAs: %v", err)
	}
	want := `sudo -n -u 'root' -- sh -lc 'journalctl -u custom -f'`
	if got != want {
		t.Fatalf("logCommandWithRunAs custom = %q, want %q", got, want)
	}
}

func TestLogCommandWithRunAsRejectsUnsafeUser(t *testing.T) {
	_, err := logCommandWithRunAs(LogOptions{Command: "id", RunAs: "root;rm"})
	if err == nil {
		t.Fatal("expected invalid run_as error")
	}
}

func TestStreamLogsAnomalyStrictInitFailure(t *testing.T) {
	err := StreamLogs(context.Background(), "", nil, LogOptions{
		Anomaly:       true,
		AnomalyModel:  "/path/does/not/exist/model.onnx",
		AnomalyStrict: true,
	}, engine.NewClientCache(), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected strict anomaly init failure")
	}
}
