# Matrix / For-Each Design Spec

## Overview
Currently, steps run exactly once per targeted host. We need the ability to dynamically expand a single step into multiple parallel steps based on a matrix of variables, similar to GitHub Actions matrix strategy.

## Architecture
We will implement "Node Expansion" before building the step execution graph.

### CUE Schema Definition
The `StepBase` in `internal/cuetry/recipe.go` and `internal/cuetry/recipe_types.go` will be extended with an optional `matrix` map.

```cue
matrix?: {[string]: [...string]}
```

### 1. Matrix Expansion Logic
A new function `ExpandMatrixSteps(r *cuetry.Recipe) error` will be called before `BuildStepGraph`.
- Iterates over all steps in `r.Steps`.
- If a step has a `matrix`, it calculates the Cartesian product of the provided keys and string arrays.
- For each combination:
  - Deep-copies the step (by marshaling/unmarshaling JSON, as the step contains polymorphic interfaces).
  - Assigns a new unique `ID`: `originalID + "[" + k=v + "," + ... + "]"` (keys sorted alphabetically).
  - Injects the matrix key-value pairs into the step's `Env` map.
- The original step is removed and replaced by the expanded nodes.
- A new field `MatrixExpansions map[string][]string` is added to `cuetry.Recipe` to track which original ID mapped to which expanded IDs.

### 2. Dependency Resolution
After expanding all steps, `ExpandMatrixSteps` must do a second pass to update `depends` arrays.
- For every step in `r.Steps`, we inspect its `depends` slice.
- If a dependency matches an original ID found in `r.MatrixExpansions`, that dependency is removed and replaced by the full list of expanded IDs.
- Thus, any downstream step waits for ALL matrix nodes to complete before starting.

### 3. Output Aggregation (`env_from`)
If a step uses `env_from: [{ step: "original_matrix_step", map: { MY_OUT: "stdout" } }]`, what happens?
- We will modify `envFromStdout` in `internal/cuetry/recipe_env_from.go` to accept the `MatrixExpansions` map.
- If it sees that the requested `refStep` was expanded, it iterates through all expanded IDs, grabs their `stdout`, and aggregates them into a JSON array string `["out1", "out2"]`.
- The downstream step receives this JSON array in the environment variable, which can be further parsed with `extract` (jq).

### Restrictions in V1
- `template: { output: "name" }` and `k8s: { output: "name" }` will NOT be allowed inside a matrix step to prevent name collisions. Validation will fail if a step contains both `matrix` and a named `output`. (Users must use `env_from: { step: "id" }` instead).
