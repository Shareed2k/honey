package engine

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// TestWrapK8sPodKVShell_empty ...
func TestWrapK8sPodKVShell_empty(t *testing.T) {
	s, err := wrapK8sPodKVShell("")
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Fatalf("expected empty, got len %d", len(s))
	}
	s, err = wrapK8sPodKVShell("   ")
	if err != nil {
		t.Fatal(err)
	}
	if s != "" {
		t.Fatalf("expected empty for whitespace-only")
	}
}

// TestWrapK8sPodKVShell_markers ...
func TestWrapK8sPodKVShell_markers(t *testing.T) {
	s, err := wrapK8sPodKVShell("echo hi")
	if err != nil {
		t.Fatal(err)
	}
	pre := 120
	if len(s) < pre {
		pre = len(s)
	}
	if !strings.Contains(s, "printf %s ") {
		t.Fatalf("expected printf %%s outer, got: %q", s[:pre])
	}
	if !strings.Contains(s, "|base64 -d|sh") {
		t.Fatal("expected base64 decode pipeline")
	}
	decoded, err := decodeOuterBootstrap(s)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, want := range []string{
		"python3",
		"PYB64",
		"HONEY_KV_URL",
		"HONEY_KV_TOKEN",
		"k8s_debug_image",
		"INB64",
		"/dev/null",
		"honey-inner-",
	} {
		if !strings.Contains(decoded, want) {
			t.Errorf("bootstrap should mention %q", want)
		}
	}
}

// decodeOuterBootstrap extracts the single-quoted payload from `printf %s '...'|...` and base64-decodes it (test-only).
func decodeOuterBootstrap(wrapped string) (string, error) {
	const prefix = "printf %s "
	if !strings.HasPrefix(wrapped, prefix) {
		return "", errors.New("decode outer: bad prefix")
	}
	rest := strings.TrimPrefix(wrapped, prefix)
	idx := strings.Index(rest, "|base64 -d|sh")
	if idx < 0 {
		return "", errors.New("decode outer: missing suffix")
	}
	q := strings.TrimSpace(rest[:idx])
	if len(q) < 2 || q[0] != '\'' || q[len(q)-1] != '\'' {
		return "", errors.New("decode outer: bad quoting")
	}
	q = q[1 : len(q)-1]
	inner := strings.ReplaceAll(q, `'\''`, "'")
	raw, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
