package config

import (
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/safepath"
)

// DefaultRecipesDirs returns the list of directories to check for default CUE recipes.
func DefaultRecipesDirs() []string {
	var dirs []string
	if base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); base != "" {
		if p, err := safepath.JoinUnder(base, "honey", "recipes"); err == nil {
			dirs = append(dirs, p)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")) == "" {
			if p, err := safepath.JoinUnder(home, ".config", "honey", "recipes"); err == nil {
				dirs = append(dirs, p)
			}
		}
	}
	return dirs
}

// ListDefaultRecipes returns a list of absolute paths to all .cue files found in the default recipe directories.
func ListDefaultRecipes() []string {
	var recipes []string
	for _, dir := range DefaultRecipesDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".cue") {
				if p, err := safepath.JoinUnder(dir, e.Name()); err == nil {
					recipes = append(recipes, p)
				}
			}
		}
	}
	return recipes
}
