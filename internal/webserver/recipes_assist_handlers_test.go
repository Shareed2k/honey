package webserver

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestCapRecipeAssistRecords(t *testing.T) {
	var recs []hosts.Record
	for i := 0; i < maxRecipeAssistRecords+10; i++ {
		recs = append(recs, hosts.Record{Name: string(rune('a' + i%26))})
	}
	out := capRecipeAssistRecords(recs)
	if len(out) != maxRecipeAssistRecords {
		t.Fatalf("len=%d want %d", len(out), maxRecipeAssistRecords)
	}
	small := []hosts.Record{{Name: "a"}}
	if capRecipeAssistRecords(small) == nil || len(capRecipeAssistRecords(small)) != 1 {
		t.Fatalf("small slice")
	}
}

func TestClipRunesForRecipeAssist(t *testing.T) {
	s := string([]rune{'a', 'b', 'c', 'd'})
	got := clipRunesForRecipeAssist(s, 3)
	if got == s || !strings.Contains(got, "truncated") {
		t.Fatalf("got %q", got)
	}
}
