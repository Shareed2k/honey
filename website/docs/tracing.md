---
id: tracing
title: Distributed Tracing
slug: /tracing
---

:::warning Not currently wired into any CLI command
`internal/engine.InitTracer` implements everything described on this page and is covered by its own unit test, but no command path (`cue-exec`, `web`, or otherwise) calls it. Setting `OTEL_EXPORTER_OTLP_ENDPOINT` and running `honey cue-exec` today produces **no traces** — `otel.Tracer("honey")` resolves to OpenTelemetry's default no-op provider. This page describes the intended design once a CLI entry point calls `InitTracer` at startup; treat it as a design reference, not a working feature, until that wiring lands.
:::

Honey supports **OpenTelemetry Distributed Tracing**, allowing you to visualize your recipe pipelines and host executions as flamegraphs in observability platforms like Jaeger, Grafana Tempo, or Honeycomb.

Tracing provides immediate visibility into:
- The overall execution time of a recipe run.
- Concurrent execution "waves" in graph recipes.
- The latency of individual step executions (SSH dials, script execution, Docker calls, etc.).
- Errors attached directly to the spans where they occurred.

## Setup

Honey uses standard OpenTelemetry environment variables to initialize the tracer provider.

To enable tracing, export the `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable pointing to your OTLP receiver (HTTP). 

For example, to send traces to a local Jaeger instance:
```bash
export OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:4318"
honey cue-exec examples/recipe/graph_parallel.cue "web-*" --execute
```

## Trace Structure

When tracing is enabled, Honey generates the following span hierarchy:

1. **`recipe.run`**: The root span encapsulating the entire lifecycle of the recipe execution. Contains attributes like `recipe.name`.
2. **`recipe.wave.<index>`**: For graph-based recipes, spans are generated for each parallel execution wave. This helps visualize concurrency bottlenecks.
3. **`step.<kind>`**: Leaf spans generated for the execution of a specific step (e.g., `step.command`, `step.docker`, `step.recipe`). Includes the `step.id` attribute.

Any errors encountered during step execution will be recorded as OpenTelemetry events and will mark the corresponding span's status as `Error`.
