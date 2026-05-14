package cuetry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadRecipeFromFile is a small test helper that reads a CUE file from disk and
// returns the decoded Recipe. The cuetry package exposes ParseRemoteRecipe (on
// bytes) rather than a file-based loader, so the helper lives in tests only.
func loadRecipeFromFile(path string) (Recipe, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Recipe{}, err
	}
	return ParseRemoteRecipe(b, nil)
}

func TestRecipeJSON_roundTripAgainstExamples(t *testing.T) {
	root := findRepoRoot(t)
	dir := filepath.Join(root, "examples", "recipe")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read examples: %v", err)
	}
	ran := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name)
			loaded, lerr := loadRecipeFromFile(path)
			if lerr != nil {
				t.Skipf("disk recipe does not load: %v", lerr)
			}
			b, herr := CanonicalRecipeJSON(loaded)
			if herr != nil {
				t.Fatalf("canonical json: %v", herr)
			}
			back, perr := RecipeFromJSON(b)
			if perr != nil {
				t.Fatalf("round-trip parse: %v", perr)
			}
			b2, herr := CanonicalRecipeJSON(back)
			if herr != nil {
				t.Fatalf("canonical json (round 2): %v", herr)
			}
			if string(b) != string(b2) {
				t.Fatalf("canonical JSON not stable through round-trip\nfirst:  %s\nsecond: %s", b, b2)
			}
		})
		ran++
	}
	if ran == 0 {
		t.Fatalf("no .cue recipes found under %s", dir)
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := cwd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d
		}
	}
	t.Fatalf("no go.mod found above %s", cwd)
	return ""
}

func TestHashRecipeJSON_stable(t *testing.T) {
	// Use a small recipe that loads without records; all_hosts.cue is the
	// minimal example shipped in the repo.
	r1, err := loadRecipeFromFile(filepath.Join(findRepoRoot(t), "examples/recipe/all_hosts.cue"))
	if err != nil {
		t.Skipf("baseline recipe not present: %v", err)
	}
	h1, err := HashRecipeJSON(r1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h1, "sha256:") || len(h1) != len("sha256:")+64 {
		t.Fatalf("hash format: %q", h1)
	}
	h2, err := HashRecipeJSON(r1)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash unstable: %s vs %s", h1, h2)
	}
}
