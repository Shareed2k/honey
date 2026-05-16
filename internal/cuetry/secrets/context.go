package secrets

import (
	"context"
	"strings"
)

type ctxRecipeDirKey struct{}

// WithRecipeDir attaches the absolute recipe directory for relative refs (e.g. age-file:).
func WithRecipeDir(ctx context.Context, absDir string) context.Context {
	return context.WithValue(ctx, ctxRecipeDirKey{}, strings.TrimSpace(absDir))
}

// RecipeDirFrom returns the directory set by [WithRecipeDir], or empty.
func RecipeDirFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxRecipeDirKey{}).(string)
	return v
}
