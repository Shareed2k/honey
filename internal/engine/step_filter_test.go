package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/hosts"
)

// allowAllFilter is a StepFilter that allows every target.
type allowAllFilter struct{ called int }

func (f *allowAllFilter) Filter(_ context.Context, targets []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	f.called++
	return targets, nil, nil
}

// skipAllFilter is a StepFilter that skips every target with a fixed reason.
type skipAllFilter struct {
	reason string
	called int
}

func (f *skipAllFilter) Filter(_ context.Context, targets []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	f.called++
	skipped := make([]HostExecResult, 0, len(targets))
	for _, t := range targets {
		sk := WhenSkippedResult(t)
		sk.Output = "(skipped: " + f.reason + ")"
		skipped = append(skipped, sk)
	}
	return nil, skipped, nil
}

// errorFilter is a StepFilter that always returns an error.
type errorFilter struct{ err error }

func (f *errorFilter) Filter(_ context.Context, _ []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	return nil, nil, f.err
}

// Verify the interface is implemented at compile time.
var (
	_ StepFilter = (*allowAllFilter)(nil)
	_ StepFilter = (*skipAllFilter)(nil)
	_ StepFilter = (*errorFilter)(nil)
)

func TestStepFilterPipeline_emptyPipelinePassesAll(t *testing.T) {
	t.Parallel()
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}}
	pipeline := NewStepFilterPipeline()
	allowed, skipped, err := pipeline.Apply(context.Background(), targets)
	require.NoError(t, err)
	assert.Equal(t, targets, allowed)
	assert.Empty(t, skipped)
}

func TestStepFilterPipeline_singleAllowFilter(t *testing.T) {
	t.Parallel()
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}}
	f := &allowAllFilter{}
	pipeline := NewStepFilterPipeline(f)
	allowed, skipped, err := pipeline.Apply(context.Background(), targets)
	require.NoError(t, err)
	assert.Equal(t, targets, allowed)
	assert.Empty(t, skipped)
	assert.Equal(t, 1, f.called)
}

func TestStepFilterPipeline_singleSkipFilter(t *testing.T) {
	t.Parallel()
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}}
	f := &skipAllFilter{reason: "policy"}
	pipeline := NewStepFilterPipeline(f)
	allowed, skipped, err := pipeline.Apply(context.Background(), targets)
	require.NoError(t, err)
	assert.Empty(t, allowed)
	assert.Len(t, skipped, 2)
	assert.Contains(t, skipped[0].Output, "policy")
}

func TestStepFilterPipeline_secondFilterReceivesOnlyAllowed(t *testing.T) {
	t.Parallel()
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}, {Name: "h3"}}
	skipFirst := &skipAllFilter{reason: "first"}
	allowAll := &allowAllFilter{}
	pipeline := NewStepFilterPipeline(skipFirst, allowAll)
	allowed, skipped, err := pipeline.Apply(context.Background(), targets)
	require.NoError(t, err)
	// All targets were skipped by the first filter; second filter sees no targets.
	assert.Empty(t, allowed)
	assert.Len(t, skipped, 3)
	// Second filter was still called (with zero targets — idempotent).
	assert.Equal(t, 1, allowAll.called)
}

func TestStepFilterPipeline_skipsAccumulateAcrossFilters(t *testing.T) {
	t.Parallel()
	// 3 targets: filter1 skips h1, filter2 skips h2 — h3 remains.
	skipH1 := &conditionalSkipFilter{skipName: "h1", reason: "f1"}
	skipH2 := &conditionalSkipFilter{skipName: "h2", reason: "f2"}
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}, {Name: "h3"}}
	pipeline := NewStepFilterPipeline(skipH1, skipH2)
	allowed, skipped, err := pipeline.Apply(context.Background(), targets)
	require.NoError(t, err)
	assert.Len(t, allowed, 1)
	assert.Equal(t, "h3", allowed[0].Name)
	assert.Len(t, skipped, 2)
}

func TestStepFilterPipeline_errorStopsEarly(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("filter exploded")
	targets := []hosts.Record{{Name: "h1"}}
	afterError := &allowAllFilter{}
	pipeline := NewStepFilterPipeline(&errorFilter{err: wantErr}, afterError)
	_, _, err := pipeline.Apply(context.Background(), targets)
	require.ErrorIs(t, err, wantErr)
	// Second filter must not be called after error.
	assert.Equal(t, 0, afterError.called)
}

// conditionalSkipFilter skips one named host.
type conditionalSkipFilter struct {
	skipName string
	reason   string
}

func (f *conditionalSkipFilter) Filter(_ context.Context, targets []hosts.Record) ([]hosts.Record, []HostExecResult, error) {
	var allowed []hosts.Record
	var skipped []HostExecResult
	for _, t := range targets {
		if t.Name == f.skipName {
			sk := WhenSkippedResult(t)
			sk.Output = "(skipped: " + f.reason + ")"
			skipped = append(skipped, sk)
		} else {
			allowed = append(allowed, t)
		}
	}
	return allowed, skipped, nil
}

// Verify riskStepFilter implements StepFilter at compile time.
var _ StepFilter = (*riskStepFilter)(nil)

func TestRiskStepFilter_safeCommand_allowsAll(t *testing.T) {
	t.Parallel()
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "test"}}
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}}
	f := NewRiskStepFilter(run, "command", "echo hello", "")
	allowed, skipped, err := f.Filter(context.Background(), targets)
	require.NoError(t, err)
	assert.Equal(t, targets, allowed)
	assert.Empty(t, skipped)
}

func TestRiskStepFilter_criticalCommand_skipsAll(t *testing.T) {
	t.Parallel()
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "test"}}
	targets := []hosts.Record{{Name: "h1"}, {Name: "h2"}}
	f := NewRiskStepFilter(run, "command", "rm -rf /", "")
	allowed, skipped, err := f.Filter(context.Background(), targets)
	require.NoError(t, err)
	assert.Empty(t, allowed)
	assert.Len(t, skipped, 2)
	for _, sk := range skipped {
		assert.Contains(t, sk.Output, "(blocked:")
	}
}

func TestRiskStepFilter_emptyCommand_passthrough(t *testing.T) {
	t.Parallel()
	run := &CueRun{Params: CueRecipeRunParams{ActorID: "test"}}
	targets := []hosts.Record{{Name: "h1"}}
	f := NewRiskStepFilter(run, "command", "", "")
	allowed, skipped, err := f.Filter(context.Background(), targets)
	require.NoError(t, err)
	assert.Equal(t, targets, allowed)
	assert.Empty(t, skipped)
}
