# E2E Tests for Recipes using Testcontainers

## Overview
We need to implement end-to-end (E2E) integration tests for the `honey` recipe engine. Currently, we have unit tests and some basic integration tests for SSH and database connections, but we lack full lifecycle tests that execute CUE recipes against real containers and assert on the execution outcomes (e.g., verifying files are created, environment variables are correctly injected, and multiline strings/prompts are handled correctly).

## Architecture

We will create a new test file: `tests/integration/recipe_e2e_test.go`.

This test file will leverage the existing `startSSH(t)` helper from `tests/integration/containers.go` to spin up a real SSH container (via `testcontainers-go`).

The E2E tests will use the real Honey HTTP API (via `newSSHTestServer(t)`) or the underlying `engine.RunCueRecipeSteps` function to execute actual CUE recipes against the test container.

## Components & Data Flow

1. **Test Fixtures (CUE Recipes)**
   We will define inline CUE recipes as Go strings within the tests, or point to existing `examples/recipe/*.cue` files if they are self-contained. Inline strings are preferred for specific regression tests (like checking LF/CR handling in environment variables).

2. **Execution Flow**
   - The test sets up the web server and SSH container (`newSSHTestServer`).
   - The test prepares an `ExecRequest` or `CueExecRequest` JSON payload.
   - The test sends the payload to `POST /api/v1/cue-exec`.
   - The test waits for the streaming NDJSON response.
   - The test aggregates the results and verifies:
     - `res.Success == true`
     - Expected strings exist in `res.Output`
     - Specific side effects occurred in the container (by running a follow-up `exec` command to `cat` a file or check state).

3. **Key Test Cases to Cover initially:**
   - **Basic Linear Recipe**: A simple recipe with 2 sequential command steps.
   - **Environment Injection**: Verifying that `env` blocks inject correctly into the SSH session.
   - **Multiline Variables & LF/CR**: Specifically testing the regression we just fixed—passing a multiline JSON string as an env var and echoing it inside the container.
   - **File Upload (PutStep)**: Creating a local temp file, using a `put` step to upload it, and verifying it exists via `exec`.

## Error Handling & Reliability
- We will use `t.Fatalf` on unexpected HTTP or API failures.
- We will use `testcontainers-go` wait strategies (already implemented in `startSSH`) to ensure the SSH daemon is ready before the recipe executes.
- Because `cue-exec` streams results line by line (NDJSON), we will use an NDJSON scanner to aggregate the `HostExecResult` structures before making assertions.

## Testing Independence
- Each subtest will instantiate its own `cueExec` request.
- The SSH container can be shared across subtests (using `t.Run`) as long as the state changes don't collide (e.g., by writing to uniquely named temporary directories inside the container).

