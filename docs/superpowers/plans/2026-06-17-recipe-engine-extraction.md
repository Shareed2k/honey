# Recipe Engine Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the CUE recipe execution logic out of the `internal/ui` package into a dedicated `internal/engine` package, communicating via an event stream.

**Architecture:** We will build `internal/engine/events.go` to define the event protocol, `internal/engine/run.go` to hold the core loop, and move execution files out of `internal/ui`. Because `internal/ui/cue_recipe_run.go` and `internal/ui/ssh_batch.go` contain deeply entangled state, we will do the extraction incrementally:
1. Move the basic shared types (`HostExecResult`, `ClientCache`, etc) to a neutral shared package or directly into `engine` and alias them in `ui`.
2. Move the streaming loop (`StreamCueRecipeSteps` and its dependents) into `engine`.
3. Switch the CLI entry points to invoke `engine.RunRecipe` and let `ui` consume the stream for rendering.

**Tech Stack:** Go 1.26+, channels, context.

---

### Task 1: Create the Event Protocol (Completed)
(Completed in earlier run)

### Task 2: Scaffold Engine Run Loop (Completed)
(Completed in earlier run)

### Task 3: Move Shared Execution Types to `engine`

Because `ui` functions (like `ssh_batch.go`) and `engine` functions will need to share vocabulary while we extract, we will move the core execution types from `internal/ui` to `internal/engine/types.go` and update `internal/ui` to point to them.

**Files:**
- Create: `internal/engine/types.go`
- Modify: `internal/ui/ssh_batch.go`, `internal/ui/cue_recipe_run.go`, etc.

- [ ] **Step 1: Move definitions to engine**
Extract `HostExecResult` and `SFTPDownloadJob` from `internal/ui/ssh_batch.go` and place them in `internal/engine/types.go`.

- [ ] **Step 2: Update UI references**
Search and replace all instances of `HostExecResult` with `engine.HostExecResult` inside `internal/ui/`.
Search and replace `SFTPDownloadJob` with `engine.SFTPDownloadJob`.

- [ ] **Step 3: Run project tests**
Run `go test ./...`. Ensure everything compiles.

- [ ] **Step 4: Commit**
`git add internal/engine/ internal/ui/ && git commit -m "refactor(engine): move shared execution types to engine"`

### Task 4: Move the Stream Coordinator to `engine`

The `cueRun` struct and the `StreamCueRecipeSteps` function are the heart of the engine, but they are currently defined in `internal/ui/cue_recipe_run.go`.

**Files:**
- Modify: `internal/engine/run.go`
- Modify: `internal/ui/cue_recipe_run.go`

- [ ] **Step 1: Extract `CueRecipeRunParams` and `cueRun` state**
Move `CueRecipeRunParams` and the `cueRun` struct (renamed to public `CueRun`) into `internal/engine/run.go`. Also move `factsScript` and `gatherFacts` to `engine`.

- [ ] **Step 2: Extract `StreamCueRecipeSteps`**
Move `StreamCueRecipeSteps` and its immediate helper functions to `internal/engine/run.go`. Because `StreamCueRecipeSteps` relies on `ClientCache`, `RecipeKVCoordinator`, and `RecipeTunnelCoordinator`, we will temporarily leave those definitions in `internal/ui` and import `internal/ui` into `engine` (or vice versa, we will need to break the cycle by moving them together).

- [ ] **Step 3: Break the cycle by moving execution dependencies**
Move `client_cache.go`, `recipe_kv.go`, `recipe_tunnel.go`, `script_runner.go`, and `ssh_batch.go` completely out of `internal/ui` and into `internal/engine`. Fix the package declarations to `package engine`.

- [ ] **Step 4: Compile and test**
Run `go build ./...` and `go test ./...`. Fix missing imports across the codebase.

- [ ] **Step 5: Commit**
`git add internal/engine/ internal/ui/ && git commit -m "refactor(engine): move streaming engine and coordinators to engine"`

### Task 5: Move Step Handlers to `engine`

Now that the core loop is in `engine`, the step implementations must follow.

**Files:**
- Move: `internal/ui/cue_recipe_docker.go` -> `internal/engine/step_docker.go`
- Move: `internal/ui/cue_recipe_k8s.go` -> `internal/engine/step_k8s.go`
- Move: `internal/ui/cue_recipe_postgres.go` -> `internal/engine/step_postgres.go`
- Move: `internal/ui/cue_recipe_plugin.go` -> `internal/engine/step_plugin.go`
- Move: `internal/ui/cue_recipe_opensearch.go` -> `internal/engine/step_opensearch.go`
- Move: `internal/ui/cue_recipe_template_exec.go` -> `internal/engine/step_template.go`
- Move: `internal/ui/cue_recipe_when.go` -> `internal/engine/step_when.go`
- Move: `internal/ui/step_retry_exec.go` -> `internal/engine/step_retry.go`

- [ ] **Step 1: Relocate and rename packages**
Move the step files into `internal/engine/` and change their package to `engine`.

- [ ] **Step 2: Update UI references**
The UI layer should no longer register or execute steps directly. Remove `cue_recipe_exec_registry.go` logic from `ui`.

- [ ] **Step 3: Compile and fix**
Run `go test ./...`.

- [ ] **Step 4: Commit**
`git add internal/engine/ internal/ui/ && git commit -m "refactor(engine): move step implementations to engine"`

### Task 6: Adapt UI to Consume Events

Now `internal/ui/cue_recipe_run.go` acts solely as the rendering adapter.

**Files:**
- Modify: `internal/ui/cue_recipe_run.go`
- Modify: `internal/ui/table.go` (if necessary)

- [ ] **Step 1: Rewrite RunCueRecipeStepsExecute**
In `internal/ui/cue_recipe_run.go`, `runCueRecipeStepsExecute` should spawn `engine.StreamCueRecipeSteps` (or `engine.RunRecipe`) and loop over `engine.HostExecResult` to render output. Note: `engine.Event` was created in Task 1, so `engine.HostExecResult` should ideally be adapted into `engine.Event` or we just use `engine.HostExecResult` natively.

- [ ] **Step 2: Fix remaining UI tests**
Update `internal/ui/cue_recipe_run_test.go` and `internal/ui/session_recorder_batch_test.go` to import from `engine`.

- [ ] **Step 3: Verify tests**
Run `go test ./...`

- [ ] **Step 4: Commit**
`git add internal/ui/ && git commit -m "feat(ui): adapt recipe runner to consume engine output"`
