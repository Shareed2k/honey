package engine

import (
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

func TestSeedDefaultMaxParallel(t *testing.T) {
	t.Parallel()

	// Config default seeds an unset recipe default.
	r := cuetry.Recipe{}
	seedDefaultMaxParallel(&r, 24)
	if r.Defaults == nil || r.Defaults.MaxParallel != 24 {
		t.Fatalf("unset recipe: got %+v, want defaults.max_parallel=24", r.Defaults)
	}

	// A recipe that already sets max_parallel is NOT overwritten.
	r2 := cuetry.Recipe{Defaults: &cuetry.RecipeDefaults{MaxParallel: 5}}
	seedDefaultMaxParallel(&r2, 24)
	if r2.Defaults.MaxParallel != 5 {
		t.Fatalf("recipe override lost: got %d, want 5", r2.Defaults.MaxParallel)
	}

	// No config default → no change (backwards-compat).
	r3 := cuetry.Recipe{}
	seedDefaultMaxParallel(&r3, 0)
	if r3.Defaults != nil && r3.Defaults.MaxParallel != 0 {
		t.Fatalf("config unset should not seed: got %+v", r3.Defaults)
	}
}

func TestSeededDefaultFlowsToHostConcurrency(t *testing.T) {
	t.Parallel()
	// End-to-end of the resolution chain: a seeded recipe default is picked up by
	// RecipeHostMaxConc for a step with no per-step max_parallel.
	r := cuetry.Recipe{}
	seedDefaultMaxParallel(&r, 20)
	step := &cuetry.PluginStep{}
	if got := RecipeHostMaxConc(step, r.Defaults); got != 20 {
		t.Fatalf("RecipeHostMaxConc = %d, want 20 (from seeded config default)", got)
	}
}
