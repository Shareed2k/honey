package ui

import "testing"

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
