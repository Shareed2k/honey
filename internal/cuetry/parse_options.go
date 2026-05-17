package cuetry

import (
	"context"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
)

// ParseOptions configures recipe parsing (optional WASM cue_transform chain).
type ParseOptions struct {
	PluginManager *plugins.Manager
}

// ParseRemoteRecipe validates cueBytes and decodes the recipe into Go values.
func ParseRemoteRecipe(cueBytes []byte, records []hosts.Record) (Recipe, error) {
	return ParseRemoteRecipeOpts(cueBytes, records, ParseOptions{})
}

// ParseRemoteRecipeOpts is like ParseRemoteRecipe with plugin transforms and prefix-aware secret validation.
func ParseRemoteRecipeOpts(cueBytes []byte, records []hosts.Record, opts ParseOptions) (Recipe, error) {
	if opts.PluginManager != nil && opts.PluginManager.Enabled() {
		var err error
		cueBytes, err = opts.PluginManager.TransformCue(context.Background(), cueBytes, len(records))
		if err != nil {
			return Recipe{}, err
		}
	}
	var prefixes []string
	if opts.PluginManager != nil {
		prefixes = opts.PluginManager.SecretRefPrefixes()
	}
	return parseRemoteRecipeAfterTransform(cueBytes, records, prefixes)
}
