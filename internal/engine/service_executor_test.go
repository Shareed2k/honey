package engine

import (
	"context"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

func TestServiceExecutor_ExecuteStream(t *testing.T) {
	recipeStr := `
recipe: {
	name: "test-svc"
	type: "graph"
	steps: [
		{
			id: "svc_test_started"
			host: "mock"
			service: { name: "cron", state: "started" }
		},
		{
			id: "svc_test_status"
			host: "mock"
			service: { name: "cron", state: "status" }
		},
		{
			id: "svc_test_enabled"
			host: "mock"
			service: { name: "cron", state: "started", enabled: true }
		},
		{
			id: "svc_test_stopped"
			host: "mock"
			service: { name: "cron", state: "stopped" }
		},
		{
			id: "svc_test_restarted"
			host: "mock"
			service: { name: "cron", state: "restarted" }
		},
		{
			id: "svc_test_reloaded"
			host: "mock"
			service: { name: "cron", state: "reloaded" }
		},
		{
			id: "svc_test_disabled"
			host: "mock"
			service: { name: "cron", state: "stopped", enabled: false }
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
			Kind:    cuetry.KindService,
			Step:    step.Step,
			Targets: []TargetContext{{Record: hosts.Record{Name: "mock"}}},
		}
		opts := ExecutionOptions{
			Reg:        reg,
			Recipe:     rec,
			CmdTimeout: 10 * time.Second,
		}

		resCh := make(chan HostExecResult, 1)
		exec := &ServiceExecutor{}
		err := exec.ExecuteStream(ctx, req, opts, resCh)
		require.NoError(t, err)

		res := <-resCh
		require.True(t, res.Success, "step %d should succeed, error: %v, output: %s", i, res.ErrMsg, res.Output)

		// Verify the script generated
		switch i {
		case 0: // started
			require.Contains(t, client.RemoteCmd, "systemctl start 'cron'")
			require.Contains(t, client.RemoteCmd, "service 'cron' start")
			require.NotContains(t, client.RemoteCmd, "systemctl enable")
		case 1: // status
			require.Contains(t, client.RemoteCmd, "systemctl status 'cron'")
			require.Contains(t, client.RemoteCmd, "service 'cron' status")
		case 2: // enabled
			require.Contains(t, client.RemoteCmd, "systemctl start 'cron'")
			require.Contains(t, client.RemoteCmd, "systemctl enable 'cron'")
		case 3: // stopped
			require.Contains(t, client.RemoteCmd, "systemctl stop 'cron'")
			require.Contains(t, client.RemoteCmd, "service 'cron' stop")
		case 4: // restarted
			require.Contains(t, client.RemoteCmd, "systemctl restart 'cron'")
			require.Contains(t, client.RemoteCmd, "service 'cron' restart")
		case 5: // reloaded
			require.Contains(t, client.RemoteCmd, "systemctl reload 'cron'")
			require.Contains(t, client.RemoteCmd, "service 'cron' reload")
		case 6: // disabled
			require.Contains(t, client.RemoteCmd, "systemctl stop 'cron'")
			require.Contains(t, client.RemoteCmd, "systemctl disable 'cron'")
		}
	}
}
