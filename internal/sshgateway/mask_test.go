package sshgateway

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// writeChunks drives w with each chunk in order, asserts the io.Writer contract
// (n == len(chunk)) on every call, then closes w to flush the retained tail.
func writeChunks(t *testing.T, w io.WriteCloser, chunks ...string) {
	t.Helper()
	for i, c := range chunks {
		n, err := w.Write([]byte(c))
		if err != nil {
			t.Fatalf("chunk %d Write error: %v", i, err)
		}
		if n != len(c) {
			t.Fatalf("chunk %d Write returned n=%d, want %d (io.Writer contract)", i, n, len(c))
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
}

func TestMaskingWriter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		values      []string
		patterns    []string
		chunks      []string
		want        string   // exact final sink contents
		mustNotHave []string // substrings that must be absent from the sink
	}{
		{
			name:   "literal in single write",
			values: []string{"s3cr3t"},
			chunks: []string{"user=admin pass=s3cr3t done"},
			want:   "user=admin pass=[MASKED] done",
		},
		{
			name:        "literal split across two writes (boundary)",
			values:      []string{"abcdef"},
			chunks:      []string{"abc", "def"},
			want:        "[MASKED]",
			mustNotHave: []string{"abcdef"},
		},
		{
			name:        "literal split with surrounding context",
			values:      []string{"topsecret"},
			chunks:      []string{"prefix-tops", "ecret-suffix"},
			want:        "prefix-[MASKED]-suffix",
			mustNotHave: []string{"topsecret"},
		},
		{
			name:     "regex in single write",
			patterns: []string{`AKIA[0-9A-Z]{16}`},
			chunks:   []string{"key AKIAIOSFODNN7EXAMPLE end"},
			want:     "key [MASKED] end",
		},
		{
			name:   "multiple overlapping values",
			values: []string{"secret", "secretvalue"},
			chunks: []string{"a secretvalue b secret c"},
			// "secret" is replaced first, so "secretvalue" -> "[MASKED]value".
			want:        "a [MASKED]value b [MASKED] c",
			mustNotHave: []string{"a secret"},
		},
		{
			name:   "value equal to another substring",
			values: []string{"pw", "pwd"},
			chunks: []string{"pwd and pw"},
			// "pw" first: "pwd" -> "[MASKED]d", "pw" -> "[MASKED]".
			want: "[MASKED]d and [MASKED]",
		},
		{
			name:   "non-secret bytes pass through unchanged",
			values: []string{"nope"},
			chunks: []string{"hello ", "world ", "no match here"},
			want:   "hello world no match here",
		},
		{
			name:     "both literal and regex",
			values:   []string{"mylit"},
			patterns: []string{`tok_[0-9]+`},
			chunks:   []string{"a mylit b tok_12345 c"},
			want:     "a [MASKED] b [MASKED] c",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rs, err := NewMaskRuleset(tt.values, tt.patterns)
			if err != nil {
				t.Fatalf("NewMaskRuleset: %v", err)
			}
			var sink bytes.Buffer
			w := NewMaskingWriter(&sink, rs)
			writeChunks(t, w, tt.chunks...)

			if got := sink.String(); got != tt.want {
				t.Errorf("final sink = %q, want %q", got, tt.want)
			}
			for _, bad := range tt.mustNotHave {
				if strings.Contains(sink.String(), bad) {
					t.Errorf("sink %q must not contain secret %q", sink.String(), bad)
				}
			}
		})
	}
}

func TestMaskingWriter_LongLiteralSplitAcrossWrites(t *testing.T) {
	t.Parallel()
	// A secret longer than one chunk, written a byte at a time, must still be
	// masked when the whole value fits within the retained window.
	secret := strings.Repeat("A", 40) + "-END"
	rs, err := NewMaskRuleset([]string{secret}, nil)
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	var sink bytes.Buffer
	w := NewMaskingWriter(&sink, rs)
	chunks := make([]string, 0, len(secret)+2)
	chunks = append(chunks, "start ")
	for _, r := range secret {
		chunks = append(chunks, string(r))
	}
	chunks = append(chunks, " finish")
	writeChunks(t, w, chunks...)

	if got := sink.String(); got != "start [MASKED] finish" {
		t.Errorf("final sink = %q, want %q", got, "start [MASKED] finish")
	}
	if strings.Contains(sink.String(), secret) {
		t.Errorf("sink leaked secret: %q", sink.String())
	}
}

func TestNewMaskRuleset_BadRegex(t *testing.T) {
	t.Parallel()
	_, err := NewMaskRuleset(nil, []string{"("})
	if err == nil {
		t.Fatal("NewMaskRuleset with bad regex: want error, got nil")
	}
	if !strings.Contains(err.Error(), "(") {
		t.Errorf("error %q should name the bad pattern", err.Error())
	}
}

func TestNewMaskRuleset_TrimDedupAndEmpty(t *testing.T) {
	t.Parallel()
	rs, err := NewMaskRuleset([]string{" a ", "a", "", "  ", "bb"}, []string{"", "  "})
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	if rs.Empty() {
		t.Fatal("ruleset with values should not be Empty")
	}
	// " a " trims to "a", duplicate of "a" dropped; empties dropped => {"a","bb"}.
	if len(rs.values) != 2 {
		t.Errorf("values = %d, want 2 (trimmed+deduped)", len(rs.values))
	}
	if rs.maxLiteral != 2 {
		t.Errorf("maxLiteral = %d, want 2", rs.maxLiteral)
	}
	if len(rs.patterns) != 0 {
		t.Errorf("patterns = %d, want 0 (empties dropped)", len(rs.patterns))
	}
}

func TestMaskRuleset_Empty(t *testing.T) {
	t.Parallel()
	rs, err := NewMaskRuleset(nil, nil)
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	if rs == nil {
		t.Fatal("empty ruleset should be non-nil")
	}
	if !rs.Empty() {
		t.Error("ruleset with no values/patterns should be Empty")
	}
}

func TestMaskingWriter_EmptyRulesetPassThrough(t *testing.T) {
	t.Parallel()
	// Both a nil ruleset and an Empty ruleset must be transparent pass-throughs:
	// bytes are emitted verbatim and nothing is held back past Close.
	empty, err := NewMaskRuleset(nil, nil)
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	for _, rs := range []*MaskRuleset{nil, empty} {
		var sink bytes.Buffer
		w := NewMaskingWriter(&sink, rs)

		// Pass-through must not buffer: each Write reaches the sink immediately.
		if _, err := w.Write([]byte("hello ")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if got := sink.String(); got != "hello " {
			t.Errorf("pass-through buffered: sink = %q, want %q before Close", got, "hello ")
		}
		if _, err := w.Write([]byte("world")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got := sink.String(); got != "hello world" {
			t.Errorf("final sink = %q, want %q", got, "hello world")
		}
	}
}

func TestMaskingWriter_WriteContractReturnsInputLength(t *testing.T) {
	t.Parallel()
	// In masking mode Write must report n == len(p) even though the masked output
	// (and thus the bytes reaching the sink) differs in length.
	rs, err := NewMaskRuleset([]string{"abc"}, nil)
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	var sink bytes.Buffer
	w := NewMaskingWriter(&sink, rs)
	// Long enough (> retain) input to force a flush inside Write, with matches so
	// the masked output length differs from the input length.
	in := []byte(strings.Repeat("abc-", 300))
	n, err := w.Write(in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Errorf("Write returned n=%d, want %d (input length, not masked length)", n, len(in))
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !strings.Contains(sink.String(), maskReplacement) {
		t.Errorf("sink should contain %q", maskReplacement)
	}
	if strings.Contains(sink.String(), "abc") {
		t.Errorf("sink leaked literal %q: %q", "abc", sink.String())
	}
}

// TestMaskingWriter_StraddlingLiteralNoLeak is the regression test for the
// boundary leak: a literal that straddles the natural flush boundary
// (len(buf)-retain) must never have its prefix flushed unmasked. It scans the
// FULL accumulated sink — including everything flushed mid-stream — for the raw
// secret.
func TestMaskingWriter_StraddlingLiteralNoLeak(t *testing.T) {
	t.Parallel()
	const secret = "SUPERSECRET" // len 11 -> retain 10
	rs, err := NewMaskRuleset([]string{secret}, nil)
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	var sink bytes.Buffer
	w := NewMaskingWriter(&sink, rs)

	// Enough non-secret bytes to force mid-stream flushes and leave a retained
	// tail, then the secret split so its first fragment lands in the retained
	// tail and straddles the next flush boundary.
	filler := strings.Repeat("A", 100)
	writeChunks(t, w, filler, "SUPER", "SECRET")

	full := sink.String()
	if !strings.Contains(full, maskReplacement) {
		t.Errorf("sink should contain %q, got %q", maskReplacement, full)
	}
	if strings.Contains(full, secret) {
		t.Fatalf("BOUNDARY LEAK: sink contains raw secret %q: %q", secret, full)
	}
	// The prefix "SUPER" must not leak on its own either (that is the exact byte
	// sequence the old flushTo-based Write would have emitted unmasked).
	if strings.Contains(full, "SUPER") {
		t.Fatalf("BOUNDARY LEAK: sink contains secret prefix %q: %q", "SUPER", full)
	}
}

// TestMaskingWriter_InteractivityPromptFlushed asserts the writer does NOT hold
// back bulk output waiting for more input: a large chunk is flushed promptly
// (before Close) and only up to `retain` bytes are ever held back.
func TestMaskingWriter_InteractivityPromptFlushed(t *testing.T) {
	t.Parallel()
	rs, err := NewMaskRuleset([]string{"secret"}, nil) // len 6 -> retain 5
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	retain := rs.maxLiteral - 1
	var sink bytes.Buffer
	w := NewMaskingWriter(&sink, rs)

	big := strings.Repeat("x", 1000)
	if _, err := w.Write([]byte(big)); err != nil {
		t.Fatalf("Write big: %v", err)
	}
	// Prompt-sized write with no trailing newline (the interactive worst case).
	if _, err := w.Write([]byte("$ ")); err != nil {
		t.Fatalf("Write prompt: %v", err)
	}

	// Output must be visible BEFORE Close (not held back until end-of-session).
	written := len(big) + len("$ ")
	if sink.Len() == 0 {
		t.Fatal("nothing flushed before Close: output was held back (unusable interactively)")
	}
	if held := written - sink.Len(); held > retain {
		t.Errorf("held back %d bytes before Close, want <= retain(%d)", held, retain)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := sink.String(); got != big+"$ " {
		t.Errorf("final sink = %q, want %q", got, big+"$ ")
	}
}

func TestMaskRuleset_SafeCut(t *testing.T) {
	t.Parallel()

	litRules, err := NewMaskRuleset([]string{"secret"}, nil)
	if err != nil {
		t.Fatalf("NewMaskRuleset(literal): %v", err)
	}
	reRules, err := NewMaskRuleset(nil, []string{`\d{4}`})
	if err != nil {
		t.Fatalf("NewMaskRuleset(regex): %v", err)
	}

	tests := []struct {
		name string
		rs   *MaskRuleset
		b    string
		cut  int
		want int
	}{
		// "secret" occupies [2,8) in "xxsecretxx".
		{name: "literal straddles: retreat to its start", rs: litRules, b: "xxsecretxx", cut: 4, want: 2},
		{name: "literal starts at cut: no straddle", rs: litRules, b: "xxsecretxx", cut: 2, want: 2},
		{name: "literal ends at cut: no straddle", rs: litRules, b: "xxsecretxx", cut: 8, want: 8},
		{name: "no straddle returns cut unchanged", rs: litRules, b: "xxsecretxx", cut: 1, want: 1},
		// `\d{4}` matches [2,6) in "ab1234cd".
		{name: "regex straddles: retreat to match start", rs: reRules, b: "ab1234cd", cut: 4, want: 2},
		{name: "regex ends at cut: no straddle", rs: reRules, b: "ab1234cd", cut: 6, want: 6},
		{name: "regex fully after cut: no straddle", rs: reRules, b: "ab1234cd", cut: 1, want: 1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.rs.safeCut([]byte(tt.b), tt.cut); got != tt.want {
				t.Errorf("safeCut(%q, %d) = %d, want %d", tt.b, tt.cut, got, tt.want)
			}
		})
	}
}

// TestMaskingWriter_AdversarialOverlapBounded proves the maskMaxBuffer cap: a
// stream that endlessly overlaps a literal ("aa" vs an unbroken run of "a")
// makes safeCut retreat to 0 on every Write, which would otherwise grow the
// buffer without bound. The force-flush at the cap must emit bytes before Close
// so memory stays bounded.
func TestMaskingWriter_AdversarialOverlapBounded(t *testing.T) {
	t.Parallel()
	rs, err := NewMaskRuleset([]string{"aa"}, nil)
	if err != nil {
		t.Fatalf("NewMaskRuleset: %v", err)
	}
	var sink bytes.Buffer
	mw := NewMaskingWriter(&sink, rs)

	// Write well past the cap as a run of 'a' (every boundary straddles "aa").
	chunk := bytes.Repeat([]byte("a"), 64*1024)
	total := 0
	for total <= maskMaxBuffer+len(chunk) {
		if _, werr := mw.Write(chunk); werr != nil {
			t.Fatalf("write: %v", werr)
		}
		total += len(chunk)
	}
	// Force-flush at the cap must have emitted output before Close (buffer is
	// bounded, not holding the whole multi-MiB stream).
	if sink.Len() == 0 {
		t.Fatal("nothing flushed before Close: buffer grew unbounded (cap not enforced)")
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Note: masking under this pathological overlap is best-effort at the forced
	// cut (documented) — the assertion here is bounded memory (output emitted
	// before Close), not perfect redaction of an adversarial infinite overlap.
}
