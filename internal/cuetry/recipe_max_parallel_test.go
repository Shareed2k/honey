package cuetry

import "testing"

func TestEffectiveMaxParallel_precedence(t *testing.T) {
	t.Parallel()
	def := &RecipeDefaults{MaxParallel: 4}
	step := RecipeStep{MaxParallel: 8}
	if got := EffectiveMaxParallel(step, def); got != 8 {
		t.Fatalf("step override: got %d", got)
	}
	step.MaxParallel = 0
	if got := EffectiveMaxParallel(step, def); got != 4 {
		t.Fatalf("defaults: got %d", got)
	}
	if got := EffectiveMaxParallel(RecipeStep{}, nil); got != 0 {
		t.Fatalf("zero: got %d", got)
	}
}

func TestValidateMaxParallelField(t *testing.T) {
	t.Parallel()
	if err := validateMaxParallelField("x", 0); err != nil {
		t.Fatal(err)
	}
	if err := validateMaxParallelField("x", 129); err == nil {
		t.Fatal("expected error")
	}
}
