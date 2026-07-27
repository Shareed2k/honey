package ui

import (
	"regexp"
	"testing"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestCueLineCommentIndex(t *testing.T) {
	cases := []struct {
		line string
		want int
	}{
		{`name: "x" // comment`, 10},
		{`no comment here`, -1},
		{`"// not a comment in string"`, -1},
		{`a: b // c`, 5},
		{`// leading`, 0},
		{`escaped: "a\"// still string"`, -1},
	}
	for _, tc := range cases {
		if got := cueLineCommentIndex(tc.line); got != tc.want {
			t.Errorf("cueLineCommentIndex(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

func TestIsCueKeyword(t *testing.T) {
	for _, w := range []string{"package", "import", "let", "for", "if", "in", "true", "false", "null"} {
		if !isCueKeyword(w) {
			t.Errorf("isCueKeyword(%q) = false, want true", w)
		}
	}
	for _, w := range []string{"name", "steps", "recipe", "Package", "forx", ""} {
		if isCueKeyword(w) {
			t.Errorf("isCueKeyword(%q) = true, want false", w)
		}
	}
}

// TestHighlightCueLine_preservesContent guards the tokenizer: highlighting wraps
// tokens in ANSI styling, but with the styling stripped the visible text must be
// byte-identical to the input — the string/keyword/comment scanning loop must
// never drop, duplicate, or reorder characters.
func TestHighlightCueLine_preservesContent(t *testing.T) {
	lines := []string{
		`package foo`,
		`name: "value" // trailing`,
		`  for x in list {`,
		`a: "esc \" quote" b`,
		``,
		`	nested: {deep: true}`,
	}
	for _, line := range lines {
		if got := stripANSI(highlightCueLine(line)); got != line {
			t.Errorf("highlightCueLine(%q) visible = %q, want identical", line, got)
		}
	}
}

func TestHighlightCueLines_perLine(t *testing.T) {
	in := []string{`package x`, `y: 1`}
	out := highlightCueLines(in)
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if stripANSI(out[i]) != in[i] {
			t.Errorf("line %d visible = %q, want %q", i, stripANSI(out[i]), in[i])
		}
	}
}
