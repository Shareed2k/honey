package engine

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitTracer_NoEndpoint(t *testing.T) {
	// Ensure env var is not set
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	ctx := context.Background()
	shutdown, err := InitTracer(ctx)

	require.NoError(t, err)
	require.NotNil(t, shutdown)

	// Call shutdown to ensure it doesn't panic
	err = shutdown(ctx)
	assert.NoError(t, err)
}

func TestInitTracer_WithEndpoint(t *testing.T) {
	// Set the env var
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")

	ctx := context.Background()
	shutdown, err := InitTracer(ctx)

	require.NoError(t, err)
	require.NotNil(t, shutdown)
	defer shutdown(ctx)
}
