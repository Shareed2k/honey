// Package recipestore provides extensible storage abstractions for managing recipe content.
package recipestore

import "context"

// RecipeMetadata represents the basic metadata of a saved CUE recipe.
type RecipeMetadata struct {
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	URL        string `json:"url,omitempty"`
	ModifiedAt int64  `json:"modified_at"`
	Size       int64  `json:"size"`
}

// RecipeStore is the core storage interface for CRUD operations on recipes.
type RecipeStore interface {
	List(ctx context.Context) ([]RecipeMetadata, error)
	Get(ctx context.Context, name string) (string, error)
	Save(ctx context.Context, name string, content string) error
	Delete(ctx context.Context, name string) error
}
