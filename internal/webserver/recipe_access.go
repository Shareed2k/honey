package webserver

import (
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/config"
)

// allowedRecipePathSetFor computes the set of absolute recipe paths a caller may
// read, parse, or run: the built-in default recipes plus each configured app's
// target_recipe (resolved against the config-file directory when relative).
//
// This is a security allowlist, so it has a single definition — the Server and
// RecipesAPI methods below both delegate here — to prevent the two views from
// drifting (a policy change applied to one copy silently leaving the other
// stale). cfg and configPath come from Options.
func allowedRecipePathSetFor(cfg *config.File, configPath string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range config.ListDefaultRecipes() {
		if cp, err := filepath.Abs(filepath.Clean(p)); err == nil {
			out[cp] = struct{}{}
		}
	}
	if cfg == nil {
		return out
	}
	for _, app := range cfg.Apps {
		p := strings.TrimSpace(app.TargetRecipe)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) && configPath != "" {
			p = filepath.Join(filepath.Dir(configPath), p)
		}
		if cp, err := filepath.Abs(filepath.Clean(p)); err == nil {
			out[cp] = struct{}{}
		}
	}
	return out
}

// sshUserFor resolves the effective SSH user: the trimmed requested value, or the
// config default when the request is empty. Shared by Server and RecipesAPI.
func sshUserFor(cfg *config.File, requested string) string {
	user := strings.TrimSpace(requested)
	if user == "" && cfg != nil && cfg.Defaults.SSHUser != "" {
		user = cfg.Defaults.SSHUser
	}
	return user
}
