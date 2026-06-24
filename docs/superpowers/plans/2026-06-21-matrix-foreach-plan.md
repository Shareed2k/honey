# Matrix / For-Each Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement a matrix expansion strategy that fans out a single step into multiple independent steps based on Cartesian combinations of variables.

**Architecture:** Node Expansion. Before the step execution graph is built, we'll replace matrix steps with N expanded steps, injecting the variables into their `Env`, and remapping downstream dependencies to wait on all N nodes.

**Tech Stack:** Go, CUE

---

### Task 1: CUE Schema and Go Types

**Files:**
- Modify: `internal/cuetry/recipe_types.go`
- Modify: `internal/cuetry/step_interface.go`
- Modify: `internal/cuetry/recipe.go`

- [ ] **Step 1: Add Matrix type and Expansion Tracking**

Modify `internal/cuetry/recipe_types.go` to add `MatrixExpansions`:
```go
type Recipe struct {
	// ... existing fields
	Steps            []StepWrapper       `json:"steps"`
	Handlers         []StepWrapper       `json:"handlers,omitempty"`
	MatrixExpansions map[string][]string `json:"-"` // internal tracking, not unmarshaled
}
```

Modify `internal/cuetry/step_interface.go` to add `Matrix` to `StepBase`:
```go
type StepBase struct {
	// ... existing fields
	Matrix      map[string][]string `json:"matrix,omitempty"`
	Assert      []Assertion         `json:"assert,omitempty"`
}
```
Add to `cloned()` in `StepBase`:
```go
	if len(b.Matrix) > 0 {
		cp.Matrix = make(map[string][]string, len(b.Matrix))
		for k, v := range b.Matrix {
			cp.Matrix[k] = slices.Clone(v)
		}
	}
```

- [ ] **Step 2: Update CUE Schema**
Modify `internal/cuetry/recipe.go` around line 60 to add `matrix`:
```cue
	matrix?: {[string]: [...string]}
```

- [ ] **Step 3: Run Linters**
Run: `go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**
```bash
git add internal/cuetry/recipe_types.go internal/cuetry/step_interface.go internal/cuetry/recipe.go
git commit -m "feat(schema): add matrix block to step schema"
```

---

### Task 2: Matrix Expansion Logic

**Files:**
- Create: `internal/cuetry/recipe_matrix.go`
- Create: `internal/cuetry/recipe_matrix_test.go`
- Modify: `internal/cuetry/recipe_graph.go`

- [ ] **Step 1: Write Expansion Tests**
Create `internal/cuetry/recipe_matrix_test.go`:
```go
package cuetry

import (
	"encoding/json"
	"testing"
)

func TestExpandMatrixSteps(t *testing.T) {
	recipeJSON := `{
		"name": "test",
		"type": "graph",
		"steps": [
			{
				"id": "setup",
				"command": "echo setup"
			},
			{
				"id": "work",
				"depends": ["setup"],
				"matrix": {"os": ["linux", "darwin"], "arch": ["amd64"]},
				"command": "echo work"
			},
			{
				"id": "cleanup",
				"depends": ["work"],
				"command": "echo cleanup"
			}
		]
	}`

	var r Recipe
	if err := json.Unmarshal([]byte(recipeJSON), &r); err != nil {
		t.Fatal(err)
	}

	if err := ExpandMatrixSteps(&r); err != nil {
		t.Fatal(err)
	}

	if len(r.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(r.Steps))
	}

	hasWorkLinux := false
	for _, w := range r.Steps {
		if w.Step.Base().ID == "work[arch=amd64,os=linux]" {
			hasWorkLinux = true
			if w.Step.Base().Env["os"] != "linux" || w.Step.Base().Env["arch"] != "amd64" {
				t.Errorf("missing or incorrect env on expanded step: %v", w.Step.Base().Env)
			}
			if len(w.Step.Base().Depends) != 1 || w.Step.Base().Depends[0] != "setup" {
				t.Errorf("incorrect depends on expanded step: %v", w.Step.Base().Depends)
			}
		}
		if w.Step.Base().ID == "cleanup" {
			if len(w.Step.Base().Depends) != 2 {
				t.Errorf("cleanup should depend on 2 expanded nodes, got: %v", w.Step.Base().Depends)
			}
		}
	}
	if !hasWorkLinux {
		t.Errorf("missing expected expanded node work[arch=amd64,os=linux]")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**
Run: `go test -v ./internal/cuetry -run TestExpandMatrixSteps`
Expected: FAIL (undefined ExpandMatrixSteps)

- [ ] **Step 3: Write Implementation**
Create `internal/cuetry/recipe_matrix.go`:
```go
package cuetry

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ExpandMatrixSteps evaluates matrix definitions and replaces them with Cartesian expanded steps.
func ExpandMatrixSteps(r *Recipe) error {
	var newSteps []StepWrapper
	r.MatrixExpansions = make(map[string][]string)

	for i, w := range r.Steps {
		b := w.Step.Base()
		if len(b.Matrix) == 0 {
			newSteps = append(newSteps, w)
			continue
		}

		if strings.TrimSpace(b.ID) == "" {
			return fmt.Errorf("step %d has matrix but no id", i)
		}

		keys := make([]string, 0, len(b.Matrix))
		for k := range b.Matrix {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var combos []map[string]string
		combos = append(combos, make(map[string]string))

		for _, key := range keys {
			vals := b.Matrix[key]
			var nextCombos []map[string]string
			for _, c := range combos {
				for _, val := range vals {
					nc := make(map[string]string, len(c)+1)
					for k, v := range c {
						nc[k] = v
					}
					nc[key] = val
					nextCombos = append(nextCombos, nc)
				}
			}
			combos = nextCombos
		}

		var expandedIDs []string
		for _, combo := range combos {
			raw, err := json.Marshal(w)
			if err != nil {
				return err
			}
			var cloneWrapper StepWrapper
			if err := json.Unmarshal(raw, &cloneWrapper); err != nil {
				return err
			}

			cb := cloneWrapper.Step.Base()
			cb.Matrix = nil
			if cb.Env == nil {
				cb.Env = make(map[string]string)
			}
			var parts []string
			for _, k := range keys {
				cb.Env[k] = combo[k]
				parts = append(parts, fmt.Sprintf("%s=%s", k, combo[k]))
			}
			newID := fmt.Sprintf("%s[%s]", b.ID, strings.Join(parts, ","))
			cb.ID = newID
			expandedIDs = append(expandedIDs, newID)
			newSteps = append(newSteps, cloneWrapper)
		}
		r.MatrixExpansions[b.ID] = expandedIDs
	}

	// Update dependencies
	for _, w := range newSteps {
		b := w.Step.Base()
		var newDeps []string
		for _, dep := range b.Depends {
			if expanded, ok := r.MatrixExpansions[dep]; ok {
				newDeps = append(newDeps, expanded...)
			} else {
				newDeps = append(newDeps, dep)
			}
		}
		b.Depends = newDeps
	}

	r.Steps = newSteps
	return nil
}
```

Modify `internal/cuetry/recipe_graph.go` inside `BuildStepGraphFromRecipe`:
```go
// Replace:
// 	if mode != ExecutionModeGraph {
// 		return nil, fmt.Errorf("cuetry: not a graph recipe")
// 	}
// 	return BuildStepGraph(r.Steps)

// With:
	if mode != ExecutionModeGraph {
		return nil, fmt.Errorf("cuetry: not a graph recipe")
	}
	if err := ExpandMatrixSteps(&r); err != nil {
		return nil, err
	}
	return BuildStepGraph(r.Steps)
```
Wait, `FormatGraphWavesText` also calls `BuildStepGraph(r.Steps)` directly.
In `internal/cuetry/recipe_graph.go`, modify `validateRecipeExecMode`:
```go
	case ExecutionModeGraph:
		if err := validateUniqueTemplateOutputs(r.Steps); err != nil {
			return err
		}
		// Expand matrices before graph validation
		if err := ExpandMatrixSteps(r); err != nil {
			return err
		}
		outputByName := templateOutputProducers(r.Steps)
```

- [ ] **Step 4: Run test to verify it passes**
Run: `go test -v ./internal/cuetry -run TestExpandMatrixSteps`
Expected: PASS

- [ ] **Step 5: Commit**
```bash
git add internal/cuetry/recipe_matrix.go internal/cuetry/recipe_matrix_test.go internal/cuetry/recipe_graph.go
git commit -m "feat(engine): implement matrix node expansion"
```

---

### Task 3: Aggregate EnvFrom Outputs

**Files:**
- Modify: `internal/cuetry/recipe_env_from.go`

- [ ] **Step 1: Modify `envFromStdout` to Aggregate Outputs**
In `internal/cuetry/recipe_env_from.go`, modify `envFromStdout`:
```go
func envFromStdout(store *StepOutputStore, capture *RecipeOutputCapture, refStep, refOut, hostName string, matrixExpansions map[string][]string) (string, error) {
	if refStep != "" {
		if store == nil {
			return "", fmt.Errorf("env_from: no output store for step %q", refStep)
		}
		
		// Handle matrix aggregation
		if expanded, ok := matrixExpansions[refStep]; ok {
			var results []string
			for _, expID := range expanded {
				var val string
				var found bool
				if hostName != "" && hostName != MatchLocalAIHost {
					val, found = store.Get(expID, hostName)
				}
				if !found {
					val, found = store.FirstStdout(expID)
				}
				if !found {
					val, found = store.Get(expID, hostName)
				}
				if found {
					results = append(results, val)
				}
			}
			if len(results) == 0 {
				return "", fmt.Errorf("env_from: step %q (matrix) has no stdout for host %q", refStep, hostName)
			}
			jsonArr, _ := json.Marshal(results)
			return string(jsonArr), nil
		}

		// Existing logic...
		var val string
		var ok bool
		// ...
```

- [ ] **Step 2: Fix compilation errors**
Because `envFromStdout` signature changed to accept `matrixExpansions map[string][]string`, update `MergeEnvFromInto` and `MergeEnvFromIntoTemplateData` to accept `recipe *Recipe` instead of `step *StepBase` alone, so they can pass `recipe.MatrixExpansions`. Or, just pass `matrixExpansions map[string][]string`.

Update signatures:
```go
func MergeEnvFromInto(dst map[string]string, step *StepBase, store *StepOutputStore, capture *RecipeOutputCapture, kv KVReader, hostName string, dryRun bool, matrixExpansions map[string][]string) error {
```
Find all calls to `MergeEnvFromInto` and update them (likely in `internal/engine/run.go`, `internal/engine/step_template.go`, etc.).

- [ ] **Step 3: Build and Test**
Run: `go build ./... && go test ./...`
Expected: PASS

- [ ] **Step 4: Commit**
```bash
git add internal/cuetry/recipe_env_from.go internal/engine/
git commit -m "feat(engine): aggregate env_from outputs for matrix steps"
```

---

### Task 4: E2E Example

**Files:**
- Create: `examples/recipe/matrix_demo.cue`

- [ ] **Step 1: Write demo recipe**
Create `examples/recipe/matrix_demo.cue`:
```cue
recipe: {
	name: "matrix-demo"
	type: "graph"
	steps: [
		{
			id: "echo-matrix"
			host: "local"
			matrix: {
				db: ["postgres", "mysql"]
				version: ["v1", "v2"]
			}
			command: "echo '{\"db\": \"\(env.db)\", \"version\": \"\(env.version)\"}'"
		},
		{
			id: "collect-results"
			depends: ["echo-matrix"]
			host: "local"
			env_from: [{
				step: "echo-matrix"
				map: ALL_RESULTS: "stdout"
			}]
			command: "echo 'Got matrix outputs: $ALL_RESULTS'"
		}
	]
}
```

- [ ] **Step 2: Run Recipe**
Run: `go run cmd/honey/main.go cue-exec examples/recipe/matrix_demo.cue "ups" --provider local --execute`
Expected: It should execute 4 matrix combinations, then the collect step should print all 4 outputs as a JSON array.

- [ ] **Step 3: Commit**
```bash
git add examples/recipe/matrix_demo.cue
git commit -m "docs(examples): add matrix expansion demo"
```
