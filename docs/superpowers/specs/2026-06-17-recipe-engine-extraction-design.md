# Spec: Extract Recipe Engine from UI

## Context
Currently, the execution engine for CUE recipes is intertwined with terminal formatting inside the `internal/ui` package (specifically in `cue_recipe_run.go` and `cue_recipe_exec_registry.go`). This creates a "God Module" that leaks domain logic across the presentation seam, making the execution logic hard to test, hard to understand, and tightly coupled to standard I/O writers.

## Architecture

We will extract the recipe execution domain into a new, presentation-agnostic `internal/engine` package.

### 1. The `internal/engine` Module (Domain)
- **Responsibility:** Execute CUE recipes against target hosts, handling concurrency, retries, script evaluation, and transport protocols.
- **Interface:** Exposes `RunRecipe(ctx context.Context, params RunParams, events chan<- Event) error`.
- **Data flow:** As steps execute, the engine emits state changes and output via the `events` channel.
- **Independence:** Will have zero imports from `internal/ui`. It will only interact with `cuetry`, `hostexec`, `hosts`, and the provider packages (`docker`, `k8s`, etc.).

### 2. The `internal/engine.Event` Type
A structured event stream to decouple execution from rendering. 
Types of events include:
- `EventStepStarted`: Signals a new recipe step is executing.
- `EventStepOutput`: Contains stdout/stderr chunks from a host.
- `EventStepCompleted`: Signals successful completion of a step on a host.
- `EventStepFailed`: Signals a failure on a host.

### 3. The `internal/ui` Module (Adapter)
- **Responsibility:** Consume the `engine.Event` stream and render it to the terminal using spinners, colors, and formatted tables.
- **Refactor:** `cue_recipe_run.go` will be drastically reduced. It will invoke `engine.RunRecipe` in a goroutine and run a `select` loop over the incoming events, updating the terminal interface accordingly.

## Testing & Verification
- Unit tests currently in `internal/ui/cue_recipe_run_test.go` will be migrated to `internal/engine/run_test.go`.
- The engine can be tested directly by asserting against the `Event` channel slice without needing to parse mock terminal strings.
