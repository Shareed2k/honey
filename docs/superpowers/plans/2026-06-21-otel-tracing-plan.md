# OpenTelemetry Tracing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Instrument recipe execution with OpenTelemetry distributed tracing to enable observability and flamegraph visualization of graph waves and steps.

**Architecture:** Initialize a global OTLP tracer provider based on standard env vars. Wrap recipe runs, graph waves, and step executions in `otel/trace` Spans.

**Tech Stack:** Go, OpenTelemetry (`go.opentelemetry.io/otel`).

---

### Task 1: Initialize Tracer Provider
**Files:**
- Create: `internal/engine/tracing.go`
- Modify: `cmd/honey/main.go` (or wherever the main entry point is to call `InitTracer`)
  Wait, we don't have access to `cmd/honey/main.go` directly here if it's not well known, we'll initialize it where it makes sense, or provide the Init function so the binary can call it. Let's provide `internal/engine/tracing.go`. Actually, we can just put the initialization inside the `engine` package and the CLI can invoke it.

- [ ] **Step 1: Write InitTracer**
Create `internal/engine/tracing.go` containing an `InitTracer(ctx context.Context) (func(context.Context) error, error)` function. Use `go.opentelemetry.io/otel/sdk/trace` and `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` to set up a provider if `OTEL_EXPORTER_OTLP_ENDPOINT` is set.
- [ ] **Step 2: Add Go Mod Dependencies**
Run `go get go.opentelemetry.io/otel go.opentelemetry.io/otel/trace go.opentelemetry.io/otel/sdk/trace go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp`.
- [ ] **Step 3: Commit**
```bash
git add internal/engine/tracing.go go.mod go.sum
git commit -m "feat(engine): add opentelemetry tracer initialization"
```

### Task 2: Instrument Run and Waves
**Files:**
- Modify: `internal/engine/run_orchestration.go`

- [ ] **Step 1: Instrument Recipe Run**
In `StreamCueRecipeSteps`, start a root span:
```go
tracer := otel.Tracer("honey")
ctx, span := tracer.Start(ctx, "recipe.run")
span.SetAttributes(attribute.String("recipe.name", p.Recipe.Name))
defer span.End()
```
Pass this augmented `ctx` down to the graph wave execution and linear execution blocks.
- [ ] **Step 2: Instrument Waves**
In `StreamCueRecipeSteps`, inside the loop over `waves`, start a span for the wave:
```go
waveCtx, waveSpan := tracer.Start(ctx, fmt.Sprintf("recipe.wave.%d", wi))
// pass waveCtx to execute steps in parallel
defer waveSpan.End()
```
- [ ] **Step 3: Commit**
```bash
git add internal/engine/run_orchestration.go
git commit -m "feat(engine): trace recipe execution and graph waves"
```

### Task 3: Instrument Step Execution
**Files:**
- Modify: `internal/engine/run.go`

- [ ] **Step 1: Instrument Step Execution**
In `StreamCueRecipeStep` (which is inside `internal/engine/run.go` or similar), start a span for the individual step:
```go
tracer := otel.Tracer("honey")
stepCtx, span := tracer.Start(ctx, fmt.Sprintf("step.%s", step.Kind()))
span.SetAttributes(attribute.String("step.id", step.Base().ID))
defer span.End()
```
Pass `stepCtx` into the executor context. Also capture any returned errors via `span.RecordError(err)` and `span.SetStatus(codes.Error, err.Error())`.
- [ ] **Step 2: Commit**
```bash
git add internal/engine/run.go
git commit -m "feat(engine): trace individual step execution"
```
