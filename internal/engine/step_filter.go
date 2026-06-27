package engine

import (
	"context"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// StepFilter is a composable per-target gate applied before step dispatch.
// Each filter receives the targets that survived all preceding filters and
// returns the subset that may proceed plus skip records for those it removed.
// Implementations are constructed with their own dependencies pre-injected so
// the pipeline itself stays ignorant of CueRun internals.
type StepFilter interface {
	Filter(ctx context.Context, targets []hosts.Record) (allowed []hosts.Record, skipped []HostExecResult, err error)
}

// StepFilterPipeline composes StepFilter implementations and applies them in
// sequence. Skips accumulate across all filters so callers receive one unified
// list of reasons. An error from any filter stops the pipeline immediately.
type StepFilterPipeline struct {
	filters []StepFilter
}

// NewStepFilterPipeline returns a pipeline that applies filters in order.
func NewStepFilterPipeline(filters ...StepFilter) *StepFilterPipeline {
	return &StepFilterPipeline{filters: filters}
}

// Apply runs each filter in sequence, accumulating skips. Returns the final
// allowed targets and all skip records from every filter.
func (p *StepFilterPipeline) Apply(ctx context.Context, targets []hosts.Record) (allowed []hosts.Record, skipped []HostExecResult, err error) {
	remaining := targets
	for _, f := range p.filters {
		var stepSkipped []HostExecResult
		remaining, stepSkipped, err = f.Filter(ctx, remaining)
		if err != nil {
			return nil, nil, err
		}
		skipped = append(skipped, stepSkipped...)
	}
	return remaining, skipped, nil
}

// policyStepFilter gates targets via the OPA step_execute decision.
type policyStepFilter struct {
	run  *CueRun
	kind string
}

func (f *policyStepFilter) Filter(ctx context.Context, targets []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	return filterTargetsByPolicy(ctx, f.run, f.kind, targets)
}

// whenStepFilter gates targets via the step's when clause.
type whenStepFilter struct {
	run  *CueRun
	step cuetry.Step
}

func (f *whenStepFilter) Filter(ctx context.Context, targets []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	if strings.TrimSpace(f.step.Base().When) == "" {
		return targets, nil, nil
	}
	kv := KvReaderFromCoordinator(f.run.RecipeKV)
	return FilterTargetsByWhen(ctx, f.run.Params.Recipe, f.step, targets, f.run.OutputStore, f.run.Params.SecretResolver, kv, f.run.Params.CLIEnv, f.run.Params.Execute)
}

// newStepFilterPipelineForRun builds the standard pre-dispatch filter pipeline
// for a recipe step: policy gate → when clause. This is the single place that
// defines filter ordering and what filters apply to every step.
func newStepFilterPipelineForRun(run *CueRun, kind string, step cuetry.Step) *StepFilterPipeline {
	return NewStepFilterPipeline(
		&policyStepFilter{run: run, kind: kind},
		&whenStepFilter{run: run, step: step},
	)
}
