package engine_test

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
)

// TestRunRecipe_Empty ...
func TestRunRecipe_Empty(t *testing.T) {
	ch := make(chan engine.Event, 1)
	err := engine.RunRecipe(context.Background(), engine.RunParams{
		Recipe: cuetry.Recipe{},
	}, ch)
	if err != nil {
		t.Fatal(err)
	}
}
