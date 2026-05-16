package cuetry

import (
	"context"

	"github.com/shareed2k/honey/internal/cuetry/secrets"
)

// WithRecipeDir attaches the absolute recipe directory to ctx (for age-file and similar).
func WithRecipeDir(ctx context.Context, absDir string) context.Context {
	return secrets.WithRecipeDir(ctx, absDir)
}
