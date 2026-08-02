package engine

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/postgres"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/policy"
)

// TargetContext binds a host record with its pre-resolved environment.
type TargetContext struct {
	Record hosts.Record
	Env    map[string]string
}

// ExecutionRequest represents a self-contained execution payload.
type ExecutionRequest struct {
	Targets    []TargetContext
	Index      int
	Step       cuetry.Step
	Kind       string
	RetryCfg   cuetry.RecipeStepRetry
	AttemptMax *atomic.Int32
	History    [][]HostExecResult
}

// ExecutionOptions provides engine-level context dependencies for specific
// executors (like sub-recipes or SSH commands) without forcing the executor
// to depend on the entire CueRun lifecycle state.
type ExecutionOptions struct {
	Execute           bool
	JSON              bool
	Recipe            cuetry.Recipe
	RecipeDir         string
	SSHUser           string
	ActorID           string
	CLIEnv            map[string]string
	AISystemPrompt    string
	SecretResolver    cuetry.SecretResolver
	PluginMgr         *plugins.Manager
	Obs               metrics.Observer
	Cache             *ClientCache
	RecipeKV          *RecipeKVCoordinator
	ConfigPath        string
	Enforcer          *policy.Enforcer
	Inventory         config.Inventory
	CmdTimeout        time.Duration
	Reg               hostexec.Registry
	Pools             *postgres.PoolManager
	Records           []hosts.Record
	OutputStore       *cuetry.StepOutputStore
	OutputCapture     *cuetry.RecipeOutputCapture
	Facts             map[string]map[string]any
	TriggeredHandlers map[string]bool
	TunnelCoord       *RecipeTunnelCoordinator
	// DockerPluginSess scopes remote runtime:docker plugin containers to the
	// run (one shim-container per plugin+host, torn down at run end). nil on
	// paths that never run remote docker plugins.
	DockerPluginSess *plugins.DockerHostSession
}

// StepExecutor defines a deep module responsible for a specific recipe step kind.
type StepExecutor interface {
	// ExecuteDryRun performs a dry run of the step, writing its plan to out.
	ExecuteDryRun(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, out io.Writer) error

	// ExecuteStream performs actual execution of the step, sending results to resCh.
	ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error
}

var (
	stepExecutorsMu sync.RWMutex
	stepExecutors   = make(map[string]StepExecutor)
)

// RegisterStepExecutor registers an executor for a specific step kind.
func RegisterStepExecutor(kind string, exec StepExecutor) {
	stepExecutorsMu.Lock()
	defer stepExecutorsMu.Unlock()
	stepExecutors[kind] = exec
}

// GetStepExecutor retrieves the executor for a given step kind.
func GetStepExecutor(kind string) (StepExecutor, error) {
	stepExecutorsMu.RLock()
	defer stepExecutorsMu.RUnlock()
	if exec, ok := stepExecutors[kind]; ok {
		return exec, nil
	}
	return nil, fmt.Errorf("no executor registered for step kind %q", kind)
}
