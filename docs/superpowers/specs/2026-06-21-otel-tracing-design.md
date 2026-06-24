# OpenTelemetry Tracing Design Spec

## Overview
We need to wrap recipe execution, graph waves, and individual steps in OpenTelemetry spans. This will allow operators to visualize recipe execution as flamegraphs in tools like Jaeger, Grafana Tempo, or Honeycomb, making it easy to identify bottlenecks and understand execution flow.

## Architecture

### 1. Dependencies
We will add direct dependencies on:
- `go.opentelemetry.io/otel`
- `go.opentelemetry.io/otel/trace`
- `go.opentelemetry.io/otel/sdk/trace`
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` (or grpc)

### 2. Trace Provider Initialization (`internal/engine/tracing.go`)
We will create a new function `InitTracer(ctx context.Context) (func(context.Context) error, error)`.
- It will read standard OTEL environment variables (e.g., `OTEL_EXPORTER_OTLP_ENDPOINT`).
- If an endpoint is configured, it will initialize an OTLP exporter and set it as the global tracer provider.
- It returns a shutdown function to be called deferentially at application exit.

### 3. Span Instrumentation
We will instrument the execution pipeline using `otel.Tracer("honey")`.

#### Root Span: Recipe Execution
In `internal/engine/run_orchestration.go` (`StreamCueRecipeSteps`):
- Start a span named `recipe.run`.
- Attributes: `recipe.name`, `recipe.type`.

#### Child Span: Graph Waves
In `internal/engine/run_orchestration.go` (inside the wave iteration loop for graph recipes):
- Start a span named `recipe.wave.<index>`.
- This span will cover the parallel execution of all steps within that wave.

#### Leaf Spans: Step Execution
In `internal/engine/run.go` (`ExecuteStep` or `StreamCueRecipeStep`):
- Start a span named `step.<id>` or `step.<kind>`.
- Attributes: `step.kind`, `step.id`.
- The context `ctx` with the span will be passed down to the executor (e.g. `ExecuteStream(sc)`).

#### Host Exec Spans (Optional but good)
Inside the specific executor (e.g., `step_docker.go` or `step_retry_exec.go`), we can create a sub-span for the actual execution against a specific host.
- Start a span named `exec.<host>`.
- Attributes: `host.name`, `host.provider`.
- Record errors (`span.RecordError`) and set span status (`codes.Error`) if the step fails.

### Context Propagation
The `StepContext` struct already contains a `context.Context`. We will ensure this context carries the active span down the execution stack so that all logs, retries, and network calls can be properly correlated if those underlying libraries also support OpenTelemetry.
