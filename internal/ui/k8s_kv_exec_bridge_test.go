package ui

import (
	"bufio"
	"bytes"
	"testing"
)

func TestReadReadyLineSkipsPTYNoise(t *testing.T) {
	in := "\x1b[?2004hroot@host:~# set -e\nREADY 8765\n"
	br := bufio.NewReader(bytes.NewBufferString(in))
	port, err := readReadyLine(br)
	if err != nil || port != 8765 {
		t.Fatalf("port=%d err=%v", port, err)
	}
}

func TestParseReadyPortEmbeddedANSI(t *testing.T) {
	line := "\x1b[?2004hroot@proxmox:~# READY 4321\r"
	port, ok := parseReadyPort(line)
	if !ok || port != 4321 {
		t.Fatalf("port=%d ok=%v", port, ok)
	}
}

func TestParseReadyPortNoMatch(t *testing.T) {
	if _, ok := parseReadyPort("just a prompt"); ok {
		t.Fatal("expected no match")
	}
}
