package ui

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestCanTrueNASTunnel(t *testing.T) {
	if !CanTrueNASTunnel(hosts.Record{
		Provider: "truenas", PrimaryIP: "10.0.0.1",
		Meta: map[string]string{"kind": "appliance"},
	}) {
		t.Fatal("appliance with IP")
	}
	if !CanTrueNASTunnel(hosts.Record{
		Provider: "truenas",
		Meta:     map[string]string{"kind": "virt_instance", "id": "x"},
	}) {
		t.Fatal("virt without IP")
	}
	if CanTrueNASTunnel(hosts.Record{Provider: "aws"}) {
		t.Fatal("aws without IP")
	}
}

func TestReadReadyLineAfterMOTD(t *testing.T) {
	br := bufio.NewReader(bytes.NewBufferString("Linux truenas\nWelcome\nREADY 8765\n"))
	if _, err := readReadyLine(br); err != nil {
		t.Fatalf("readReady: %v", err)
	}
}

func TestDrainAPIShellReaderIdle(t *testing.T) {
	var buf bytes.Buffer
	_, _ = buf.WriteString("banner line\n")
	br := bufio.NewReader(&buf)
	if err := drainAPIShellReader(context.Background(), br, 500*time.Millisecond, 50*time.Millisecond); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

func TestTrueNASDialBridgeBootstrapUsesRawPTYAndExportsEnv(t *testing.T) {
	cmd := trueNASDialBridgeBootstrap("10.0.0.5", "8007")
	for _, want := range []string{
		"stty raw -echo min 1 time 0",
		"export HONEY_REMOTE_HOST='10.0.0.5' HONEY_REMOTE_PORT='8007'",
		"exec python3 -u /tmp/honey-tcp-dial-bridge.py",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("bootstrap missing %q in %q", want, cmd)
		}
	}
}
