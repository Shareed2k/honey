package webserver

import (
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestModelIDsSortedFromList(t *testing.T) {
	list := openai.ModelsList{
		Models: []openai.Model{
			{ID: "b"},
			{ID: "a"},
			{ID: "b"},
		},
	}
	got := modelIDsSortedFromList(list)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %#v", got)
	}
}
