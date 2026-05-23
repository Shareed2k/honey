package ui

import (
	"bufio"
	"bytes"
	"context"
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
