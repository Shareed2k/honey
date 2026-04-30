package cuetry

import (
	"path/filepath"
	"testing"
)

func TestResolveLocalAgainstRecipe(t *testing.T) {
	dir := "/project/recipes"
	got, err := ResolveLocalAgainstRecipe(dir, "files/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "files", "a.txt")
	if filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("got %q want %q", got, want)
	}
	abs := "/tmp/x"
	got2, err := ResolveLocalAgainstRecipe(dir, abs)
	if err != nil || got2 != filepath.Clean(abs) {
		t.Fatalf("%q %v", got2, err)
	}
}
