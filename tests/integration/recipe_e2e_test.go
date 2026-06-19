//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestRecipeE2E_LinearExecution(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

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

	client, err := reg.Dialer.Dial("testuser", sshH, sshP, keyFile)
	require.NoError(t, err)
	defer client.Close()

	out, err := client.Run("ls /tmp/e2e_test_file")
	require.NoError(t, err)
	assert.Equal(t, "/tmp/e2e_test_file\n", string(out))
}

func TestRecipeE2E_MultilineEnvVar(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

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

func TestRecipeE2E_PutStep(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

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
		Recipe:    recipe,
		RecipeDir: tmpDir,
		Records:   []hosts.Record{rec},
		SSHUser:   "testuser",
		Execute:   true,
		Reg:       reg,
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

func TestRecipeE2E_ScriptStep(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)
	reg := &testRegistry{Dialer: newTestDialer(sshH, sshP, keyFile)}
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

	tmpDir := t.TempDir()
	localScript := filepath.Join(tmpDir, "test.sh")
	err := os.WriteFile(localScript, []byte("#!/bin/sh\necho \"Script executed successfully\"\n"), 0755)
	require.NoError(t, err)

	cueContent := `
recipe: {
	name: "test-script-step"
	type: "linear"
	steps: [
		{
			host: "*"
			script: {
				local: "` + localScript + `"
				remote: "/tmp/remote_script.sh"
			}
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

	require.Len(t, results, 1)
	assert.True(t, results[0].Success, "Expected success: %s", results[0].ErrMsg)
	assert.Contains(t, results[0].Output, "Script executed successfully")
}

func TestRecipeE2E_GetStep(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)
	reg := &testRegistry{Dialer: newTestDialer(sshH, sshP, keyFile)}
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

	tmpDir := t.TempDir()

	cueContent := `
recipe: {
	name: "test-get-step"
	type: "linear"
	steps: [
		{
			host: "*"
			command: "echo 'file contents' > /tmp/to_download.txt"
		},
		{
			host: "*"
			get: {
				remote: "/tmp/to_download.txt"
				local: "` + tmpDir + `"
			}
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
		Recipe:    recipe,
		RecipeDir: tmpDir,
		Records:   []hosts.Record{rec},
		SSHUser:   "testuser",
		Execute:   true,
		Reg:       reg,
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
	assert.True(t, results[1].Success, "Expected success: %s", results[1].ErrMsg)

	// Since we download to a directory, it will be saved as "ssh-test_to_download.txt"
	// based on the CueGetLocalIsDirectory logic.
	downloadedFile := filepath.Join(tmpDir, "ssh-test_to_download.txt")
	content, err := os.ReadFile(downloadedFile)
	require.NoError(t, err)
	assert.Equal(t, "file contents\n", string(content))
}

type testSecretResolver map[string]string

func (m testSecretResolver) Handles(ref string) bool {
	_, ok := m[ref]
	return ok
}

func (m testSecretResolver) Resolve(_ context.Context, ref string) (string, error) {
	if val, ok := m[ref]; ok {
		return val, nil
	}
	return "", fmt.Errorf("secret not found")
}

func TestRecipeE2E_PostgresStep(t *testing.T) {
	pgDSN := startPostgres(t)
	// Create an empty registry, database steps do not require ssh dialers
	reg := &testRegistry{}
	rec := hosts.Record{Provider: "test", Name: "db-test"}

	cueContent := `
recipe: {
	name: "test-postgres-step"
	type: "linear"
	steps: [
		{
			host: "*"
			postgres: {
				dsn_secret: "my_db"
				action: "query"
				sql: "SELECT 1 AS val"
			}
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Mock SecretResolver to return our container's DSN
	secretResolver := testSecretResolver{
		"my_db": pgDSN,
	}

	outCh := make(chan engine.HostExecResult, 10)
	params := engine.CueRecipeRunParams{
		Recipe:         recipe,
		Records:        []hosts.Record{rec},
		Execute:        true,
		Reg:            reg,
		SecretResolver: secretResolver,
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
	assert.True(t, results[0].Success, "Expected success: %s", results[0].ErrMsg)
	assert.Contains(t, results[0].Output, "val") // Ensure the column name was dumped
}

func TestRecipeE2E_OpenSearchStep(t *testing.T) {
	osURL := startOpenSearch(t)
	reg := &testRegistry{}
	rec := hosts.Record{Provider: "test", Name: "os-test"}

	cueContent := `
recipe: {
	name: "test-opensearch-step"
	type: "linear"
	steps: [
		{
			host: "*"
			opensearch: {
				addresses: ["` + osURL + `"]
				username: "admin"
				password: "Qx7#nBm2pLv!"
				insecure: true
				index: "test-index"
				action: "index"
				doc_id: "1"
				body: {
					"title": "hello"
				}
			}
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

	require.Len(t, results, 1)
	assert.True(t, results[0].Success, "Expected success: %s", results[0].ErrMsg)
	assert.Contains(t, results[0].Output, "indexed")
}
