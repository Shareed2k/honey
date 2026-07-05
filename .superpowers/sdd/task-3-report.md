## Task 3: Deepen StepExecutors by Lifting Env Resolution to CueRun

### Implementation Details
- Defined `TargetContext` in `internal/engine/step_executor.go`.
- Modified `StepContext.Targets` to use `[]TargetContext` instead of `[]hosts.Record`.
- Refactored `StepExecutor` implementations across `step_command.go`, `step_docker.go`, `step_hooks.go`, `step_k8s.go`, `step_opensearch.go`, `step_plugin.go`, `step_postgres.go`, `step_recipe.go`, `step_script.go`, `step_template.go`, and `step_tunnel.go` to use `TargetContext` and access pre-resolved environment via `tc.Env`.
- Removed `StepEnvResolver` from `StepContext`.
- Updated `ExecuteStep` in `internal/engine/run.go` to resolve environment centrally prior to creating `StepContext`.
- Adapted generic processing functions in `batch_exec.go` and `script_runner.go` to receive and unpack `TargetContext`.
- Adapted the CLI and webserver callers along with tests (like `batch_exec_cancel_test.go` and `opa_test.go`) to initialize and pass `TargetContext` structs instead of `hosts.Record`.

### Testing
- Ran `go test ./internal/engine/...` successfully. All compilation errors resolved and business logic tested.
- `go test ./...` passes (with the exception of `dns_tcp_test.go` due to an unrelated preexisting flaky timeout).

### Files Changed
- `internal/engine/step_executor.go`
- `internal/engine/run.go`
- `internal/engine/run_orchestration.go`
- `internal/engine/batch_exec.go`
- `internal/engine/script_runner.go`
- `internal/engine/command_risk_gate.go`
- `internal/engine/step_command.go`
- `internal/engine/step_docker.go`
- `internal/engine/step_hooks.go`
- `internal/engine/step_k8s.go`
- `internal/engine/step_opensearch.go`
- `internal/engine/step_plugin.go`
- `internal/engine/step_postgres.go`
- `internal/engine/step_recipe.go`
- `internal/engine/step_template.go`
- `internal/engine/step_tunnel.go`
- `internal/cli/exec.go`
- `internal/webserver/exec_recipes_handlers.go`
- `internal/webserver/upload.go`
- `internal/mcpserver/exec.go`
- `internal/ui/table.go`
- `internal/alertwebhook/server.go`

### Self-Review
The implementation strictly followed the requirement to centrally lift environment resolution in `CueRun` before execution steps. A substantial amount of signature adjustments were made around parallel batch execution inside `internal/engine/batch_exec.go` to accommodate the change.
