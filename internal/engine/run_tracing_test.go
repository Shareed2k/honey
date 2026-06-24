package engine

import (
	"context"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStreamCueRecipeStep_Tracing(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := trace.NewTracerProvider(trace.WithSpanProcessor(sr))
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(trace.NewTracerProvider())

	run := &CueRun{
		Params: CueRecipeRunParams{
			Recipe: cuetry.Recipe{
				Name: "tracing-recipe",
			},
			Records: []hosts.Record{{Name: "host1"}},
		},
		OutputStore: cuetry.NewStepOutputStore(),
	}

	step := &cuetry.CommandStep{
		StepBase: cuetry.StepBase{
			ID: "step-123",
		},
		Command: "echo ok",
	}

	ctx := context.Background()
	outCh := make(chan HostExecResult, 1)

	// Since we are testing tracing, we don't necessarily need a real executor,
	// but StreamCueRecipeStep will attempt to execute it.
	// We can use a test executor or just let it fail. If it fails, we should see an error span.
	_, _ = StreamCueRecipeStep(ctx, run, 0, step, nil, outCh)

	spans := sr.Ended()
	require.NotEmpty(t, spans, "expected at least one span to be recorded")

	var stepSpan trace.ReadOnlySpan
	for _, s := range spans {
		if s.Name() == "step.command" {
			stepSpan = s
			break
		}
	}
	require.NotNil(t, stepSpan, "expected to find a span named 'step.command'")

	// Verify attributes
	var foundID bool
	for _, attr := range stepSpan.Attributes() {
		if attr.Key == attribute.Key("step.id") {
			assert.Equal(t, "step-123", attr.Value.AsString())
			foundID = true
		}
	}
	assert.True(t, foundID, "expected span to have 'step.id' attribute")
}
