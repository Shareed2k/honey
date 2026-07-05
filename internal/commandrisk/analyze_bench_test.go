package commandrisk

import (
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func BenchmarkSplitArgs(b *testing.B) {
	words := []*syntax.Word{
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "docker"}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "run"}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "-d"}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "--name"}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "test"}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "nginx"}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "-p"}}},
		{Parts: []syntax.WordPart{&syntax.Lit{Value: "80:80"}}},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = splitArgs(words)
	}
}
