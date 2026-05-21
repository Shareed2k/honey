package truenasshell

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestParseExitMarker(t *testing.T) {
	code, found, tail := parseExitMarker([]byte("hello\n__HONEY_EXIT__0\n"))
	if !found || code != 0 {
		t.Fatalf("parse: found=%v code=%d", found, code)
	}
	if got := string(stripCommandOutput(tail)); got != "hello" {
		t.Fatalf("tail=%q stripped=%q", tail, got)
	}
	code2, found2, tail2 := parseExitMarker([]byte("err\n__HONEY_EXIT__2\n"))
	if !found2 || code2 != 2 {
		t.Fatalf("code2=%d found=%v", code2, found2)
	}
	if got := string(stripCommandOutput(tail2)); got != "err" {
		t.Fatalf("tail2=%q stripped=%q", tail2, got)
	}
}

func TestFindExitMarkerSplitAcrossReads(t *testing.T) {
	buf := []byte("out\n__HONEY_EXIT__")
	buf = append(buf, "1\n"...)
	code, found, out := findExitMarkerInBuffer(buf)
	if !found || code != 1 {
		t.Fatalf("found=%v code=%d", found, code)
	}
	if string(out) != "out\n" {
		t.Fatalf("out=%q", out)
	}
}

func TestFindExitMarkerIgnoresEchoedWrapperPercentD(t *testing.T) {
	// PTY echo of wrapRemoteCommand contains __HONEY_EXIT__%d before the real exit line.
	buf := []byte("noise\nstty -echo; printf '\\n__HONEY_EXIT__%d\\n' \"$?\"\n")
	buf = append(buf, []byte("__HONEY_OUT_BEGIN__\nseed: ok\n__HONEY_OUT_END__\n__HONEY_EXIT__0\n")...)
	code, found, out := findExitMarkerInBuffer(buf)
	if !found || code != 0 {
		t.Fatalf("found=%v code=%d", found, code)
	}
	got := string(extractScriptOutput(out))
	if got != "seed: ok" {
		t.Fatalf("out=%q extracted=%q", out, got)
	}
}

func TestWrapRemoteCommandUsesBashScript(t *testing.T) {
	s := wrapRemoteCommand("set -e\necho hi")
	if strings.Contains(s, "set -e\necho") {
		t.Fatal("multi-line cmd must not be sent raw to PTY")
	}
	if !strings.Contains(s, "base64 -d") || !strings.Contains(s, "--noprofile --norc -s") {
		t.Fatal("expected base64|bash --noprofile --norc -s wrapper")
	}
}

func TestExtractScriptOutputBetweenMarkers(t *testing.T) {
	raw := "root@proxmox-q-device:~# noise\n__HONEY_OUT_BEGIN__\nseed: wrote graph_seed_x\n__HONEY_OUT_END__\n"
	got := string(extractScriptOutput([]byte(raw)))
	if got != "seed: wrote graph_seed_x" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractScriptOutputIgnoresUptimeOutsideMarkers(t *testing.T) {
	raw := " 16:57:33 up 2 days, load average: 2.26\n__HONEY_OUT_BEGIN__\nseed: wrote y\n__HONEY_OUT_END__\n"
	got := string(extractScriptOutput([]byte(raw)))
	if strings.Contains(got, "load average") {
		t.Fatalf("uptime outside markers must be dropped: %q", got)
	}
	if got != "seed: wrote y" {
		t.Fatalf("got %q", got)
	}
}

func TestRecordSupportsAPIShell(t *testing.T) {
	if !RecordSupportsAPIShell(hosts.Record{Provider: "truenas", Meta: map[string]string{"kind": "vm", "virt_instance_id": "x"}}) {
		t.Fatal("expected vm supported")
	}
	if RecordSupportsAPIShell(hosts.Record{Provider: "aws", PrimaryIP: "1.2.3.4"}) {
		t.Fatal("expected aws false")
	}
}

func TestWrapRemoteCommand(t *testing.T) {
	s := wrapRemoteCommand("echo hi")
	if !strings.Contains(s, honeyExitMarker) {
		t.Fatal("missing marker")
	}
}

func TestStripCommandOutputDropsMOTD(t *testing.T) {
	raw := "Linux truenas 6.12\nWelcome to TrueNAS\nseed: wrote graph_seed_x\n"
	got := string(stripCommandOutput([]byte(raw)))
	if strings.Contains(got, "Welcome to TrueNAS") {
		t.Fatalf("MOTD not stripped: %q", got)
	}
	if got != "seed: wrote graph_seed_x" {
		t.Fatalf("got %q", got)
	}
}

func TestStripCommandOutputPromptGlued(t *testing.T) {
	raw := "root@proxmox-backup-server:~# seed: wrote graph_seed_x\n"
	got := string(stripCommandOutput([]byte(raw)))
	if got != "seed: wrote graph_seed_x" {
		t.Fatalf("got %q", got)
	}
}

func TestStripCommandOutputPromptOnly(t *testing.T) {
	raw := "root@proxmox-backup-server:~#\n"
	got := string(stripCommandOutput([]byte(raw)))
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestStripCommandOutputKeepsUptime(t *testing.T) {
	raw := " 16:51:08 up 2 days,  8:27, 33 users,  load average: 1.95, 1.82, 1.95\n"
	got := string(stripCommandOutput([]byte(raw)))
	if !strings.Contains(got, "load average") {
		t.Fatalf("uptime line dropped: %q", got)
	}
}

func TestStripCommandOutputKeepsTruenasInCommand(t *testing.T) {
	raw := "echo connected to truenas api\n"
	got := string(stripCommandOutput([]byte(raw)))
	if got != "echo connected to truenas api" {
		t.Fatalf("got %q", got)
	}
}
