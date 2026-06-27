package engine

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// StepContext bundles the per-step arguments for executing or dry-running a
// recipe step. Run is always set and is the single source of run-scoped inputs
// (recipe, records, env, secret resolver, plugin manager, …) via Run.Params;
// the remaining fields are specific to this step and invocation.
type StepContext struct {
	Ctx         context.Context
	Run         *CueRun
	Out         io.Writer
	Targets     []hosts.Record
	Index       int
	Step        cuetry.Step
	Kind        string
	RetryCfg    cuetry.RecipeStepRetry
	AttemptMax  *atomic.Int32
	ResultCh    chan<- HostExecResult
	History     [][]HostExecResult
	EnvResolver StepEnvResolver // resolves effective env per step/target; never nil after construction
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
