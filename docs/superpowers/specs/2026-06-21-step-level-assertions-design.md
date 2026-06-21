# Step-Level Assertions Design Spec

## Overview
Currently, `hostctl` determines step success purely by exit codes (e.g., `0` is success). We need the ability to assert success based on the stdout/stderr payload (using regex or JSON paths) or even expect a specific non-zero exit code. 

## Architecture
We will implement an Engine-Level Post-Processing approach. The core engine will evaluate assertions after any executor (SSH, Docker, K8s, Plugin) finishes its work.

### CUE Schema Definition
The `StepBase` in `internal/cuetry/recipe.go` and `internal/cuetry/recipe_types.go` will be extended with an `assert` array.

```cue
assert?: [...{
    regex?: string
    not_regex?: string
    json_path?: string
    expected_value?: string // optional, used with json_path
    exit_code?: int
}]
```

### Evaluation Logic (`internal/engine/assertions.go`)
A new module will house the `EvaluateAssertions(result *HostExecResult, assertions []cuetry.Assertion) error` function.

#### Supported Assertions:
1. **Exit Code**: Checks if `result.ExitCode` exactly matches the expected `exit_code`. If this assertion is present, it will **override** the default success check. (e.g., if a script exits 1, but we assert `exit_code: 1`, the result is forced to `Success: true`).
2. **Regex Match**: Uses `regexp.MatchString(rule.Regex, result.Output)`. If no match, fails.
3. **Negative Regex Match**: Uses `!regexp.MatchString(rule.NotRegex, result.Output)`. If match, fails.
4. **JSON Path**: 
   - Uses `github.com/tidwall/gjson`.
   - Parses `result.Output`.
   - Checks if the path exists.
   - If `expected_value` is provided, compares the stringified result of the path to `expected_value`.

### Engine Integration
In `internal/engine/step_executor.go` (specifically where `RunHostExecWithRetry` invokes the step executor function and gets the result), we will inject a call to `EvaluateAssertions`. 
If any assertion returns an error:
- `result.Success` is set to `false`.
- `result.ErrMsg` is prefixed with the assertion error.

## Scope
This is fully contained within the CUE schema and the execution engine. It requires no changes to individual transport executors (docker, k8s, ssh) because it operates on the uniform `HostExecResult` they all produce.
