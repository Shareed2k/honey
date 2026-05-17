package cuetry

import "fmt"

const (
	minRecipeMaxParallel = 1
	maxRecipeMaxParallel = 128
)

// EffectiveMaxParallel returns host-level parallelism for a step (SSH/SFTP batch).
// Step max_parallel overrides defaults; zero means caller should use its package default (32).
func EffectiveMaxParallel(step RecipeStep, defaults *RecipeDefaults) int {
	if step.MaxParallel > 0 {
		return step.MaxParallel
	}
	if defaults != nil && defaults.MaxParallel > 0 {
		return defaults.MaxParallel
	}
	return 0
}

func validateMaxParallelField(where string, n int) error {
	if n == 0 {
		return nil
	}
	if n < minRecipeMaxParallel || n > maxRecipeMaxParallel {
		return fmt.Errorf("cuetry: %s.max_parallel must be between %d and %d", where, minRecipeMaxParallel, maxRecipeMaxParallel)
	}
	return nil
}
