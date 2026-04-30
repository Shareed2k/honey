package cuetry

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ResolveLocalAgainstRecipe returns an absolute local path: absolute paths are
// unchanged; relative paths are joined to recipeDir.
func ResolveLocalAgainstRecipe(recipeDir, local string) (string, error) {
	local = strings.TrimSpace(local)
	if local == "" {
		return "", fmt.Errorf("empty local path")
	}
	recipeDir = strings.TrimSpace(recipeDir)
	if recipeDir == "" {
		return "", fmt.Errorf("empty recipe directory")
	}
	if filepath.IsAbs(local) {
		return filepath.Clean(local), nil
	}
	return filepath.Clean(filepath.Join(recipeDir, local)), nil
}
