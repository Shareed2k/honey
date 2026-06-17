package engine

import (
	"context"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
)

// RunParams holds inputs for executing a recipe across hosts.
type RunParams struct {
	Recipe         cuetry.Recipe
	RecipeDir      string
	Records        []hosts.Record
	SSHUser        string
	Execute        bool
	CliEnv         map[string]string
	ConfigPath     string
	SecretResolver cuetry.SecretResolver
	PluginMgr      *plugins.Manager
}

// RunRecipe executes a recipe and emits lifecycle events.
func RunRecipe(_ context.Context, _ RunParams, events chan<- Event) error {
	defer close(events)
	// placeholder for migrating cue_recipe_run.go logic
	return nil
}
