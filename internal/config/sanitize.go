package config

import (
	"context"

	"github.com/go-playground/mold/v4/modifiers"
)

var conform = modifiers.New()

// Sanitize trims whitespace from all string fields tagged with mod:"trim".
// Called automatically by Load and ParseYAML before Validate.
func (f *File) Sanitize() {
	_ = conform.Struct(context.Background(), f)
}
