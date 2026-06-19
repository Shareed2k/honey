package engine

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
)

// StepContext bundles all arguments required to execute or dry-run a recipe step,
// eliminating long parameter lists and deep coupling.
type StepContext struct {
	Ctx            context.Context
	Run            *CueRun
	Out            io.Writer
	Recipe         cuetry.Recipe
	RecipeDir      string
	Records        []hosts.Record
	Targets        []hosts.Record
	SSHUser        string
	Execute        bool
	CLIEnv         map[string]string
	ConfigPath     string
	Index          int
	Step           cuetry.Step
	Kind           string
	SecretResolver cuetry.SecretResolver
	PluginMgr      *plugins.Manager
	RetryCfg       cuetry.RecipeStepRetry
	AttemptMax     *atomic.Int32
	ResultCh       chan<- HostExecResult
	AISystemPrompt string
	History        [][]HostExecResult
}

// StepExecutor defines a deep module responsible for a specific recipe step kind.
type StepExecutor interface {
	// ExecuteDryRun performs a dry run of the step, writing its plan to sc.Out.
	ExecuteDryRun(sc *StepContext) error

	// ExecuteStream performs actual execution of the step, sending results to sc.ResultCh.
	ExecuteStream(sc *StepContext) error
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
