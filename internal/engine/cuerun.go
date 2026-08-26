package engine

import (
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/postgres"
)

// CueRecipeRunParams ...
type CueRecipeRunParams struct {
	Recipe         cuetry.Recipe
	RecipeDir      string
	Records        []hosts.Record
	SSHUser        string
	ActorID        string // caller identity for OPA policy input; "" resolves to "api"
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
	Cache          *ClientCache     // optional shared cache; nil = create a fresh per-run cache
	Enforcer       *policy.Enforcer // optional OPA host-filter gate; nil = allow all
	Inventory      config.Inventory // config inventory; resolved per-host into OPA host_vars
	CmdTimeout     time.Duration    // per-host command timeout; 0 = none
	MaxParallel    int              // config default host fan-out (1-128); 0 = per-step defaults
}

// CueRun ...
type CueRun struct {
	Params            CueRecipeRunParams
	Cache             *ClientCache
	RecipeKV          *RecipeKVCoordinator
	TunnelCoord       *RecipeTunnelCoordinator
	InterceptCoord    *RecipeInterceptCoordinator
	DockerPluginSess  *plugins.DockerHostSession
	OutputStore       *cuetry.StepOutputStore
	OutputCapture     *cuetry.RecipeOutputCapture
	Facts             map[string]map[string]any
	TriggeredHandlers map[string]bool
}
