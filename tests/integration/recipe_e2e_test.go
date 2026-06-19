//go:build integration

package integration

import (
	"context"
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
