# hostctl — Domain Glossary

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
recipe-execution lifecycle: parse → validate prompts → resolve secrets →
(dry-run plan | streaming execute) → session recording. Its seam is
`RunRequest → (plan string | <-chan HostExecResult)`. Callers (webserver,
scheduler, webhooks) translate their own inputs into a `RunRequest` and consume
the result; they own no execution policy.

**RunRequest** — the high-level input to `RecipeRunner`: recipe bytes, target
records, env, ssh user, recording flags. Deliberately raw (bytes, not a
pre-parsed `Recipe`) so all pre-execution logic concentrates behind the seam.

**PluginProvider** (`internal/engine`) — the seam for acquiring a plugin
`*plugins.Manager` for one run, plus a release func. Exists because plugin
lifecycle differs across callers:
- webserver sync path borrows a *shared, ref-counted* manager (`pluginCache`)
  that is reused, not closed per request.
- scheduler/webhook async paths open a *fresh* manager per run and close it.

Two real adapters → a real seam, not a hypothetical one.

**StepExecutor** — interface in `internal/engine` dispatched by step `Kind()`.
Each executor resolves its own env via `cuetry.EffectiveEnvForRunEx` (a known
friction point — see architecture reviews).

**SessionRecorder** — captures a recipe run (plan, per-host results, errors) to
`RecordDir` for later replay/inspection. Owned by `RecipeRunner` during a run.

## Consumers (thin adapters over RecipeRunner)

**webserver** (`internal/webserver`) — HTTP API. `handleCueExec` (sync + NDJSON
stream), webhook handlers, assist handlers.

**scheduler** (`internal/scheduler`) — cron-driven recipe execution; submits runs
to a `queue.Queue` for async execution, detached from the cron tick context.
