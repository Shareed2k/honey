package anomaly

import (
	"testing"
)

func BenchmarkLCSLength(b *testing.B) {
	seqA := []string{"this", "is", "a", "test", "sequence", "for", "lcs", "length", "computation"}
	seqB := []string{"this", "is", "another", "test", "sequence", "for", "the", "lcs", "length", "computation"}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = lcsLength(seqA, seqB)
	}
}

func BenchmarkAlignTemplates(b *testing.B) {
	seqA := []string{"this", "is", "a", "test", "sequence", "for", "lcs", "length", "computation"}
	seqB := []string{"this", "is", "another", "test", "sequence", "for", "the", "lcs", "length", "computation"}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = alignTemplates(seqA, seqB)
	}
}
