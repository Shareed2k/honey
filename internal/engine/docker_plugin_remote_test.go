package engine

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

// remoteFreePortAllocator mirrors the internal/plugins package's unexported
// freeLoopbackPortAllocator interface structurally. Go interfaces are
// structurally typed, so *dockerPluginSSHBackend satisfying THIS interface
// (defined here, in package engine) is equivalent to it satisfying the
// plugins package's interface of the identical shape — the type assertion
// plugins.dockerTransport.readyFor performs (`backend.(freeLoopbackPortAllocator)`)
// succeeds at runtime purely on method-set shape, with no import relationship
// required between the two packages. We can't write
// `var _ plugins.freeLoopbackPortAllocator = (*dockerPluginSSHBackend)(nil)`
// directly because that interface is unexported in internal/plugins; this is
// the behavioral-conformance equivalent, compiled here instead.
type remoteFreePortAllocator interface {
	FreeLoopbackPort(ctx context.Context) (int, error)
}

var _ remoteFreePortAllocator = (*dockerPluginSSHBackend)(nil)

func TestIsRemoteHostRecord(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want bool
	}{
		{"local ai host sentinel", "-", false},
		{"loopback ip", "127.0.0.1", false},
		{"localhost name", "localhost", false},
		{"empty ip", "", false},
		{"real ipv4", "10.1.2.3", true},
		{"public ip", "203.0.113.7", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRemoteHostRecord(hosts.Record{PrimaryIP: tt.ip})
			if got != tt.want {
				t.Errorf("isRemoteHostRecord(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestNormalizeDockerArch(t *testing.T) {
	tests := []struct {
		name    string
		uname   string
		want    string
		wantErr bool
	}{
		{"x86_64", "x86_64\n", "amd64", false},
		{"amd64 alias", "amd64", "amd64", false},
		{"aarch64", "aarch64\n", "arm64", false},
		{"arm64 alias", "  arm64  ", "arm64", false},
		{"unsupported", "riscv64", "", true},
		{"empty", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeDockerArch(tt.uname)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeDockerArch(%q) expected error, got %q", tt.uname, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeDockerArch(%q): %v", tt.uname, err)
			}
			if got != tt.want {
				t.Errorf("normalizeDockerArch(%q) = %q, want %q", tt.uname, got, tt.want)
			}
		})
	}
}

// TestParseRemoteFreePort_Valid proves parseRemoteFreePort extracts the port
// number remoteFreePortScript prints to stdout, tolerating the trailing
// newline an SSH command's combined output normally carries.
func TestParseRemoteFreePort_Valid(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want int
	}{
		{"trailing newline", "54321\n", 54321},
		{"no newline", "1", 1},
		{"surrounding whitespace", "  65535  \n", 65535},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRemoteFreePort([]byte(tt.out))
			if err != nil {
				t.Fatalf("parseRemoteFreePort(%q): %v", tt.out, err)
			}
			if got != tt.want {
				t.Errorf("parseRemoteFreePort(%q) = %d, want %d", tt.out, got, tt.want)
			}
		})
	}
}

// TestParseRemoteFreePort_Garbage proves parseRemoteFreePort rejects anything
// that isn't a single valid TCP port number — a python traceback (missing
// interpreter, permission denied, etc.) must surface as an error, not silently
// decode to port 0 or a garbage int that later fails opaquely inside Docker's
// NetworkMode:"host" readiness probe.
func TestParseRemoteFreePort_Garbage(t *testing.T) {
	tests := []struct {
		name string
		out  string
	}{
		{"empty", ""},
		{"traceback", "Traceback (most recent call last):\n  File \"<string>\", line 1\n"},
		{"zero", "0"},
		{"negative", "-1"},
		{"too large", "70000"},
		{"non-numeric", "python3: command not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := parseRemoteFreePort([]byte(tt.out)); err == nil {
				t.Errorf("parseRemoteFreePort(%q) = %d, <nil>, want an error", tt.out, got)
			}
		})
	}
}

// TestStdoutForRecord_PrefersStdoutOverOutput proves env_from/stepStdout
// consumers get the stderr-free Stdout field when an executor populated it
// (plugin steps) instead of the stderr-mixed Output every step kind has
// always provided — the bug behind a JSON-output plugin action whose process
// also logs to stderr (e.g. watchtower) corrupting output_format: "json"
// downstream via env_from.extract.
func TestStdoutForRecord_PrefersStdoutOverOutput(t *testing.T) {
	withStdout := HostExecResult{Output: `{"ok":true}` + "\ntime=\"...\" level=info msg=\"noisy\"", Stdout: `{"ok":true}`}
	if got := stdoutForRecord(withStdout); got != `{"ok":true}` {
		t.Errorf("stdoutForRecord = %q, want the clean Stdout field", got)
	}

	withoutStdout := HostExecResult{Output: "plain command output, no Stdout populated"}
	if got := stdoutForRecord(withoutStdout); got != withoutStdout.Output {
		t.Errorf("stdoutForRecord = %q, want fallback to Output for kinds without a separate Stdout", got)
	}
}

func TestDockerPluginHostKey_StablePerHost(t *testing.T) {
	a := hosts.Record{Provider: "aws", Name: "web-1", PrimaryIP: "10.0.0.1"}
	aSameValues := hosts.Record{Provider: "aws", Name: "web-1", PrimaryIP: "10.0.0.1"}
	b := hosts.Record{Provider: "aws", Name: "web-2", PrimaryIP: "10.0.0.2"}
	if dockerPluginHostKey(a) == dockerPluginHostKey(b) {
		t.Fatal("distinct hosts must produce distinct keys")
	}
	if dockerPluginHostKey(a) != dockerPluginHostKey(aSameValues) {
		t.Fatal("two records with the same field values must produce the same key")
	}
}
