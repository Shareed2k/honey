package sshgateway

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// maskReplacement is the token substituted for every matched secret in the
// target→client output stream (both the live stream and the recording).
const maskReplacement = "[MASKED]"

// regexLookback bounds the extra tail the writer holds back when regex patterns
// are configured, so a regex match split across two Writes is caught within this
// window. It is kept small to preserve interactivity: regex masking on the live
// stream adds up to this many bytes of latency, and a regex match longer than
// this that straddles a write boundary is best-effort (may not be masked in the
// live stream, though Flush at end-of-session still masks the final buffer).
// Literal masking needs no such window — maxLiteral-1 bytes of lookback fully
// covers any split literal (see NewMaskingWriter).
const regexLookback = 256

// maskMaxBuffer hard-caps the writer's held-back buffer. safeCut can, on a
// pathological/adversarial stream that endlessly overlaps a literal (e.g. the
// literal "aa" against an unbroken run of "a"), keep retreating to 0 and never
// find a safe boundary — which would grow the buffer without bound (a memory
// DoS on untrusted target output). When the buffer exceeds this cap and nothing
// is safely flushable, Write force-flushes the masked prefix at the provisional
// boundary anyway: memory stays bounded, at the cost of a possible secret split
// exactly at this rare forced cut (documented, best-effort). Real secrets are
// sparse, so normal streams never reach this.
const maskMaxBuffer = 1 << 20 // 1 MiB

// MaskRuleset is a compiled set of redaction rules: exact literal secret values
// and regular-expression patterns. It is safe for concurrent reads (immutable
// after NewMaskRuleset).
type MaskRuleset struct {
	values     [][]byte
	patterns   []*regexp.Regexp
	maxLiteral int
}

// NewMaskRuleset compiles values and patterns into a ruleset. Literal values are
// trimmed, empties dropped, and duplicates removed; maxLiteral tracks the longest
// literal (used to size the writer's boundary-safety window). Each pattern is
// compiled with regexp.Compile; a bad pattern yields a wrapped error naming it.
// An empty values+patterns input yields a non-nil ruleset whose Empty() is true.
func NewMaskRuleset(values []string, patterns []string) (*MaskRuleset, error) {
	rs := &MaskRuleset{}
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		rs.values = append(rs.values, []byte(v))
		if len(v) > rs.maxLiteral {
			rs.maxLiteral = len(v)
		}
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("compile mask pattern %q: %w", p, err)
		}
		rs.patterns = append(rs.patterns, re)
	}
	return rs, nil
}

// Empty reports whether the ruleset carries no literals and no patterns, in
// which case wrapping is a pure pass-through.
func (rs *MaskRuleset) Empty() bool {
	return len(rs.values) == 0 && len(rs.patterns) == 0
}

// mask returns a masked copy of b: every occurrence of each literal value is
// replaced first, then every regex match. It never mutates b (bytes.ReplaceAll
// and regexp.ReplaceAll both return fresh slices), so the caller's retained tail
// is never corrupted.
func (rs *MaskRuleset) mask(b []byte) []byte {
	out := append([]byte(nil), b...)
	repl := []byte(maskReplacement)
	for _, v := range rs.values {
		out = bytes.ReplaceAll(out, v, repl)
	}
	for _, pat := range rs.patterns {
		out = pat.ReplaceAll(out, repl)
	}
	return out
}

// safeCut returns the largest boundary <= cut at which no literal value and no
// regex match straddles, so mask(b[:safeCut]) never flushes a partial secret
// (whose remainder is still buffered and would leak unmasked). It retreats to
// the start of the earliest straddling match and re-checks, walking down to 0 if
// every candidate boundary is unsafe.
func (rs *MaskRuleset) safeCut(b []byte, cut int) int {
	for cut > 0 {
		earliest := cut
		for _, v := range rs.values {
			lo := cut - len(v) + 1
			if lo < 0 {
				lo = 0
			}
			// An occurrence straddles cut iff it starts in [lo, cut-1]; the
			// window [lo, hi) is exactly wide enough to hold such an occurrence.
			hi := min(len(b), cut+len(v)-1)
			if i := bytes.Index(b[lo:hi], v); i >= 0 && lo+i < cut {
				if lo+i < earliest {
					earliest = lo + i
				}
			}
		}
		for _, pat := range rs.patterns {
			for _, m := range pat.FindAllIndex(b, -1) {
				if m[0] < cut && m[1] > cut && m[0] < earliest {
					earliest = m[0]
				}
			}
		}
		if earliest == cut {
			return cut
		}
		cut = earliest
	}
	return 0
}

// maskingWriter is a streaming redactor. It masks the target→client output both
// in the live stream and in the recording by wrapping the writer that tees to
// both. It holds back a bounded tail so a secret split across writes is still
// masked (see Write). A nil rules field makes it a transparent pass-through.
type maskingWriter struct {
	w      io.Writer
	rules  *MaskRuleset
	buf    []byte
	retain int
}

// NewMaskingWriter wraps w so writes are masked by rules before reaching w. When
// rules is nil or Empty, the returned writer is a transparent pass-through
// (Write forwards directly, Close is a no-op), so callers may wrap
// unconditionally. Close flushes the retained tail without closing w (the caller
// owns w).
//
// retain is the small lookback held back for boundary safety: maxLiteral-1 bytes
// (all a split literal can ever need), widened to regexLookback only when regex
// patterns are configured. It is deliberately tiny so interactive output (shell
// prompts, recent lines) is not held back — see Write.
func NewMaskingWriter(w io.Writer, rules *MaskRuleset) io.WriteCloser {
	if rules == nil || rules.Empty() {
		return &maskingWriter{w: w}
	}
	retain := rules.maxLiteral - 1
	if len(rules.patterns) > 0 {
		retain = max(retain, regexLookback)
	}
	if retain < 0 {
		retain = 0
	}
	return &maskingWriter{w: w, rules: rules, retain: retain}
}

// Write appends p to the internal buffer and flushes the masked prefix that lies
// before the retained tail. The provisional boundary (len(buf)-retain) is pulled
// back by safeCut to a point at which no secret straddles it, so we never flush a
// secret's prefix unmasked (its remainder is still buffered) — that boundary leak
// is what safeCut prevents. The unflushed tail is re-scanned on the next Write
// (or masked whole in Flush), so a secret split across writes is still masked.
//
// Per the io.Writer contract it reports n = len(p) (the input consumed), not the
// masked output length; on an underlying write error it returns that error.
func (m *maskingWriter) Write(p []byte) (int, error) {
	if m.rules == nil {
		return m.w.Write(p)
	}
	m.buf = append(m.buf, p...)
	if len(m.buf) <= m.retain {
		// Hold everything back until we exceed the lookback window.
		return len(p), nil
	}
	cut := m.rules.safeCut(m.buf, len(m.buf)-m.retain)
	if cut <= 0 {
		// A secret straddles every candidate boundary; nothing is safely
		// flushable yet — hold and re-scan on the next Write / Flush. But bound
		// memory: if the buffer has grown past the cap (pathological overlap),
		// force-flush the masked prefix at the provisional boundary anyway.
		if len(m.buf) <= maskMaxBuffer {
			return len(p), nil
		}
		cut = len(m.buf) - m.retain
	}
	masked := m.rules.mask(m.buf[:cut])
	_, err := m.w.Write(masked)
	// Keep the unmasked tail (re-scanned next time); reuse the backing array.
	m.buf = append(m.buf[:0], m.buf[cut:]...)
	return len(p), err
}

// Flush masks and writes any buffered bytes, then clears the buffer.
func (m *maskingWriter) Flush() error {
	if m.rules == nil || len(m.buf) == 0 {
		return nil
	}
	masked := m.rules.mask(m.buf)
	_, err := m.w.Write(masked)
	m.buf = nil
	return err
}

// Close flushes the retained tail. It deliberately does NOT close the underlying
// writer even when it implements io.Closer — the caller owns w. It returns the
// flush error.
func (m *maskingWriter) Close() error {
	return m.Flush()
}
