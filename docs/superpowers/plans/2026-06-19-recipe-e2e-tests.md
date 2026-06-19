# Recipe E2E Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement E2E integration tests for CUE recipes using `testcontainers-go` and the `honey` engine.

**Architecture:** We will create a new test file `tests/integration/recipe_e2e_test.go` that spins up a test SSH container, executes inline CUE recipes via the engine, and asserts on the NDJSON stream output and actual side effects in the container.

**Tech Stack:** Go, testing, testcontainers-go

---

### Task 1: Basic E2E Test Setup and Linear Recipe Execution

**Files:**
- Create: `tests/integration/recipe_e2e_test.go`

- [ ] **Step 1: Write the failing test for a simple linear recipe**

```go
//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestRecipeE2E_LinearExecution(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	// Create a registry matching the integration test SSH setup
	reg := &hostexec.StandardRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := sshTestRecord(sshH, sshP)

	cueContent := `
recipe: {
	name: "test-linear"
	type: "linear"
	steps: [
		{
			host: "*"
			command: "echo hello E2E"
		},
		{
			host: "*"
			command: "touch /tmp/e2e_test_file"
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)
	
	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		SSHUser: "testuser",
		Execute: true,
		Reg:     reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.Contains(t, results[0].Output, "hello E2E")
	assert.True(t, results[1].Success)

	// Verify side effect
	client, err := reg.Dialer.Dial("testuser", sshH, sshP, "")
	require.NoError(t, err)
	defer client.Close()

	out, err := client.Run("ls /tmp/e2e_test_file")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/e2e_test_file\n", string(out))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags=integration ./tests/integration -run TestRecipeE2E_LinearExecution -v`
Expected: Passes! (The engine is already implemented, we are just writing tests)
*Wait, actually this will fail compilation if we miss imports or pass bad params. We should run it to make sure it compiles and passes.*

- [ ] **Step 3: Commit**

```bash
git add tests/integration/recipe_e2e_test.go
git commit -m "test(e2e): add basic linear recipe execution e2e test"
```

---

### Task 2: Multiline Environment Variable Regression Test

**Files:**
- Modify: `tests/integration/recipe_e2e_test.go`

- [ ] **Step 1: Write test for multiline env vars (LF/CR)**

Append to `tests/integration/recipe_e2e_test.go`:

```go
func TestRecipeE2E_MultilineEnvVar(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	reg := &hostexec.StandardRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := sshTestRecord(sshH, sshP)

	cueContent := `
recipe: {
	name: "test-multiline-env"
	type: "linear"
	steps: [
		{
			host: "*"
			command: "echo \"$JSON_PAYLOAD\""
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)
	
	// Simulating the exact CLIEnv behavior that failed before
	cliEnv := map[string]string{
		"JSON_PAYLOAD": "{\n  \"key\": \"value\"\n}",
	}

	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		SSHUser: "testuser",
		Execute: true,
		CLIEnv:  cliEnv,
		Reg:     reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 1)
	assert.True(t, results[0].Success, "Expected success, got error: %s", results[0].ErrMsg)
	assert.Contains(t, results[0].Output, "{\n  \"key\": \"value\"\n}")
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -tags=integration ./tests/integration -run TestRecipeE2E_MultilineEnvVar -v`
Expected: PASS (verifies our recent fix)

- [ ] **Step 3: Commit**

```bash
git add tests/integration/recipe_e2e_test.go
git commit -m "test(e2e): add multiline env var regression test"
```

---

### Task 3: File Upload (Put) Step E2E Test

**Files:**
- Modify: `tests/integration/recipe_e2e_test.go`

- [ ] **Step 1: Write test for file uploads using put step**

Append to `tests/integration/recipe_e2e_test.go`:

```go
import (
	"os"
	"path/filepath"
)
// Ensure these imports are at the top

func TestRecipeE2E_PutStep(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	reg := &hostexec.StandardRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := sshTestRecord(sshH, sshP)

	// Create a temporary local file to upload
	tmpDir := t.TempDir()
	localFile := filepath.Join(tmpDir, "upload_test.txt")
	err := os.WriteFile(localFile, []byte("hello from local"), 0644)
	require.NoError(t, err)

	cueContent := `
recipe: {
	name: "test-put-step"
	type: "linear"
	steps: [
		{
			host: "*"
			put: {
				local: "` + localFile + `"
				remote: "/tmp/uploaded_test.txt"
			}
		},
		{
			host: "*"
			command: "cat /tmp/uploaded_test.txt"
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)
	
	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		SSHUser: "testuser",
		Execute: true,
		Reg:     reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeSteps(ctx, params, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 2)
	assert.True(t, results[0].Success) // Put step success
	assert.True(t, results[1].Success) // Command step success
	assert.Contains(t, results[1].Output, "hello from local")
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test -tags=integration ./tests/integration -run TestRecipeE2E_PutStep -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add tests/integration/recipe_e2e_test.go
git commit -m "test(e2e): add put step file upload e2e test"
```
