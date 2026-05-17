package hosts

import "testing"

func TestMetaSSHIdentityFile_andClone(t *testing.T) {
	t.Parallel()
	r := Record{Name: "h", PrimaryIP: "1.2.3.4"}
	if _, ok := MetaSSHIdentityFile(&r); ok {
		t.Fatal("expected no meta")
	}
	out := CloneWithMetaSSHIdentityFile(r, "~/.ssh/recipe_key")
	id, ok := MetaSSHIdentityFile(&out)
	if !ok || id != "~/.ssh/recipe_key" {
		t.Fatalf("got %q ok=%v", id, ok)
	}
	if r.Meta != nil {
		t.Fatal("input mutated")
	}
}
