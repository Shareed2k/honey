package engine

import (
	"context"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

func TestPackageExecutor_ExecuteStream(t *testing.T) {
	recipeStr := `
recipe: {
	name: "test-pkg"
	type: "graph"
	steps: [
		{
			id: "pkg_test_present"
			host: "mock"
			package: { name: "nginx", state: "present" }
		},
		{
			id: "pkg_test_absent"
			host: "mock"
			package: { name: "nginx", state: "absent" }
		}
	]
}
`
	rec, err := cuetry.ParseRemoteRecipe([]byte(recipeStr), nil)
	require.NoError(t, err)

	client := &MockHostClient{}
	reg := &MockRegistry{Client: client}

	ctx := context.Background()

	for i, step := range rec.Steps {
		req := ExecutionRequest{
			Index:   i,
			Kind:    cuetry.KindPackage,
			Step:    step.Step,
			Targets: []TargetContext{{Record: hosts.Record{Name: "mock"}}},
		}
		opts := ExecutionOptions{
			Reg:        reg,
			Recipe:     rec,
			CmdTimeout: 10 * time.Second,
		}

		resCh := make(chan HostExecResult, 1)
		exec := &PackageExecutor{}
		err := exec.ExecuteStream(ctx, req, opts, resCh)
		require.NoError(t, err)

		res := <-resCh
		require.True(t, res.Success, "step %d should succeed, error: %v, output: %s", i, res.ErrMsg, res.Output)

		// Verify the script generated
		if i == 0 { // present
			require.Contains(t, client.RemoteCmd, "apt-get install -y -qq 'nginx'")
			require.Contains(t, client.RemoteCmd, "dnf install -y -q 'nginx'")
		} else { // absent
			require.Contains(t, client.RemoteCmd, "apt-get remove -y -qq 'nginx'")
			require.Contains(t, client.RemoteCmd, "dnf remove -y -q 'nginx'")
		}
	}
}
