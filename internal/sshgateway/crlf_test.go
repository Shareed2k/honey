package sshgateway

import (
	"bytes"
	"testing"
)

func TestCRLFWriter(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{"bare LF cooked", []string{"a\nb\n"}, "a\r\nb\r\n"},
		{"existing CRLF preserved", []string{"a\r\nb\r\n"}, "a\r\nb\r\n"},
		{"lone CR preserved", []string{"a\rb"}, "a\rb"},
		{"consecutive LFs", []string{"\n\n"}, "\r\n\r\n"},
		{"no newline unchanged", []string{"plain text"}, "plain text"},
		{"CRLF split across writes not doubled", []string{"a\r", "\nb"}, "a\r\nb"},
		{"LF at write boundary cooked", []string{"a", "\n", "b"}, "a\r\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := newCRLFWriter(&buf)
			for _, s := range tt.writes {
				n, err := w.Write([]byte(s))
				if err != nil {
					t.Fatalf("write %q: %v", s, err)
				}
				if n != len(s) {
					t.Errorf("Write(%q) = %d, want %d (io.Writer contract: input length)", s, n, len(s))
				}
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}
