package engine

import (
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/postgres"
)

// CueRecipeRunParams ...
type CueRecipeRunParams struct {
	Recipe         cuetry.Recipe
	RecipeDir      string
	Records        []hosts.Record
	SSHUser        string
	CLIEnv         map[string]string
	ConfigPath     string
	AISystemPrompt string
	SecretResolver cuetry.SecretResolver
	PluginMgr      *plugins.Manager
	Execute        bool
	JSON           bool
	Reg            hostexec.Registry
	Obs            metrics.Observer
	Pools          *postgres.PoolManager
}

// CueRun ...
type CueRun struct {
	Params            CueRecipeRunParams
	Cache             *ClientCache
	RecipeKV          *RecipeKVCoordinator
	TunnelCoord       *RecipeTunnelCoordinator
	OutputStore       *cuetry.StepOutputStore
	OutputCapture     *cuetry.RecipeOutputCapture
	Facts             map[string]map[string]any
	TriggeredHandlers map[string]bool
}
