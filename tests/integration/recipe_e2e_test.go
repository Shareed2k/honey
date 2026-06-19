//go:build integration

package integration

import (
	"context"
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
