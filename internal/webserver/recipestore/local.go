package recipestore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
)

// LocalRecipeStore implements RecipeStore by reading/writing from/to a local filesystem directory.
type LocalRecipeStore struct {
	dir string
}

// NewLocalRecipeStore creates a LocalRecipeStore with the specified base directory.
func NewLocalRecipeStore(dir string) *LocalRecipeStore {
	return &LocalRecipeStore{dir: dir}
}

// List returns metadata of all recipes in the local store.
func (s *LocalRecipeStore) List(_ context.Context) ([]RecipeMetadata, error) {
	if s.dir == "" {
		return nil, fmt.Errorf("local recipe store: directory not configured")
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []RecipeMetadata{}, nil
		}
		return nil, err
	}

	var list []RecipeMetadata
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(entry.Name()), ".cue") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			list = append(list, RecipeMetadata{
				Name:       entry.Name(),
				Path:       filepath.Join(s.dir, entry.Name()),
				ModifiedAt: info.ModTime().Unix(),
				Size:       info.Size(),
			})
		}
	}
	return list, nil
}

// Get retrieves the CUE content of a local recipe by its filename.
func (s *LocalRecipeStore) Get(_ context.Context, name string) (string, error) {
	name, err := normalizeCueRecipeName(name)
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.dir, name)
	b, err := safepath.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Save creates or replaces a local recipe file with the specified content.
func (s *LocalRecipeStore) Save(_ context.Context, name string, content string) error {
	name, err := normalizeCueRecipeName(name)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}

	finalContent := []byte(content)
	if strings.HasPrefix(strings.TrimSpace(content), "{") {
		existingCUE, err := safepath.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		merged, err := cuetry.ApplyJSONToCUEAST(existingCUE, []byte(content))
		if err != nil {
			return fmt.Errorf("failed to apply JSON to CUE AST: %w", err)
		}
		finalContent = merged
	}

	// #nosec G703
	return os.WriteFile(path, finalContent, 0o600)
}

// Delete permanently removes a local recipe file by name.
func (s *LocalRecipeStore) Delete(_ context.Context, name string) error {
	name, err := normalizeCueRecipeName(name)
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, name)
	err = os.Remove(path)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

func normalizeCueRecipeName(name string) (string, error) {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == "" {
		return "", fmt.Errorf("recipe name is required")
	}
	if !strings.HasSuffix(strings.ToLower(name), ".cue") {
		return "", fmt.Errorf("recipe filename must end with .cue")
	}
	return name, nil
}
