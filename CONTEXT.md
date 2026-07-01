# honey — Domain Glossary

> Mental model for the codebase. Names here are load-bearing — use them exactly
> in code, comments, and design discussions. Architecture vocabulary (module,
> interface, depth, seam, adapter, leverage, locality) follows `codebase-design`.

## Core concepts

**Recipe** — a CUE file declaring `steps` (commands, scripts, k8s/docker exec,
sub-recipes, AI) to run against target hosts. Parsed by `internal/cuetry` into a
`cuetry.Recipe`. May declare `defaults.prompts`, `defaults.env`,
`defaults.secrets`, and `schedules`.

**Step** — one unit of a recipe (`cuetry.Step`, polymorphic via `Kind()`).
Executed by a `StepExecutor` in `internal/engine`. Execution lives in the engine,
not on the step interface (import-cycle reason; see memory).

**Host / Record** — a target machine (`hosts.Record`): SSH host, k8s pod, or
docker container. Recipes expand `host: "*"` globs into concrete records.

## Execution

**RecipeRunner** (`internal/engine`) — the deep module that owns the full
recipe-execution lifecycle: parse → resolve targets → manage plugins → validate prompts
→ resolve secrets → (dry-run plan | streaming execute) → session recording. Its seam is
`RunRequest → (plan string | <-chan HostExecResult)`. Callers (webserver,
scheduler, webhooks) translate their own inputs into a declarative `RunRequest` and consume
the result; they own no orchestration or execution policy.

**CommandRunner** (`internal/engine`) — a specialized, deep module alongside `RecipeRunner` for ad-hoc remote execution. Takes a declarative `CommandRunRequest` and handles metrics, session recording, and connection streams internally, keeping caller adapters (like the webserver) thin.

**RunRequest** — the high-level input to `RecipeRunner`. Deliberately declarative
(raw bytes/path, target pattern, plugin policy) so all parsing, target resolution,
and plugin lifecycle management concentrates behind the seam.

**PluginProvider** (`internal/engine`) — the internal mechanism for acquiring a plugin
`*plugins.Manager` for one run. Managed entirely by the `RecipeRunner` based on the
run's plugin policy (shared vs fresh).

**StepExecutor** — interface in `internal/engine` dispatched by step `Kind()`.
Accepts an **ExecutionRequest**, a deep, self-contained payload that isolates the
executor from the broader engine state. Executors are pure functions of this request.

**SessionRecorder** — captures a recipe run (plan, per-host results, errors) to
`RecordDir` for later replay/inspection. Owned by `RecipeRunner` during a run.

## Consumers (thin adapters over RecipeRunner)

**webserver** (`internal/webserver`) — HTTP API. `handleCueExec` (sync + NDJSON
stream), webhook handlers, assist handlers.

**scheduler** (`internal/scheduler`) — cron-driven recipe execution; submits runs
to a `queue.Queue` for async execution, detached from the cron tick context.
