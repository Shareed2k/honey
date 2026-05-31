package cli

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// TestPrintEgressInstructions tests that printEgressInstructions prints the
// SOCKS5 address and host name.
func TestPrintEgressInstructions(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printEgressInstructions([]string{"my-bastion"}, "127.0.0.1", 1080)

	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "127.0.0.1:1080") {
		t.Errorf("expected SOCKS5 addr in output, got:\n%s", out)
	}
	if !strings.Contains(out, "my-bastion") {
		t.Errorf("expected host name in output, got:\n%s", out)
	}
}

// TestIsValidHostname tests the isValidHostname helper function.
func TestIsValidHostname(t *testing.T) {
	valid := []string{"localhost", "192.168.1.1", "host.example.com", "a"}
	for _, h := range valid {
		if !isValidHostname(h) {
			t.Errorf("expected %q to be valid hostname", h)
		}
	}
	invalid := []string{"", strings.Repeat("a", 254), "host..double"}
	for _, h := range invalid {
		if isValidHostname(h) {
			t.Errorf("expected %q to be invalid hostname", h)
		}
	}
}

// TestParseEgressArg tests the parseEgressArg helper.
func TestParseEgressArg(t *testing.T) {
	tests := []struct {
		arg        string
		wantHost   string
		wantWeight int
	}{
		{"my-bastion", "my-bastion", 1},
		{"my-bastion:3", "my-bastion", 3},
		{"192.168.1.1:2", "192.168.1.1", 2},
		{"host:0", "host:0", 1},   // weight 0 is invalid, treated as no-weight
		{"host:-1", "host:-1", 1}, // negative weight treated as no-weight
		{"host:", "host:", 1},     // empty suffix treated as no-weight
	}
	for _, tc := range tests {
		host, weight := parseEgressArg(tc.arg)
		if host != tc.wantHost || weight != tc.wantWeight {
			t.Errorf("parseEgressArg(%q) = (%q, %d), want (%q, %d)",
				tc.arg, host, weight, tc.wantHost, tc.wantWeight)
		}
	}
}

// TestPrintEgressInstructions_MultiHost tests that printEgressInstructions
// lists all hosts and mentions round-robin when more than one host is given.
func TestPrintEgressInstructions_MultiHost(t *testing.T) {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	printEgressInstructions([]string{"us-bastion", "eu-bastion", "ap-bastion"}, "127.0.0.1", 1080)

	_ = w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	out := buf.String()

	if !strings.Contains(out, "127.0.0.1:1080") {
		t.Errorf("expected SOCKS5 addr in output, got:\n%s", out)
	}
	for _, name := range []string{"us-bastion", "eu-bastion", "ap-bastion"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected host %q in output, got:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "round-robin") {
		t.Errorf("expected round-robin mention in multi-host output, got:\n%s", out)
	}
}

// TestConfigureSystemSOCKSProxy_NonDarwin tests that configureSystemSOCKSProxy
// is a no-op (returns nil error and a non-nil cleanup) on non-Darwin systems.
func TestConfigureSystemSOCKSProxy_NonDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("testing non-darwin path only")
	}
	cleanup, err := configureSystemSOCKSProxy("127.0.0.1", 1080)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup func should not be nil")
	}
	cleanup() // should not panic
}
