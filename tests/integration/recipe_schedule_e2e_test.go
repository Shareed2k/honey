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

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"
)

type scheduleTestProvider struct {
	rec hosts.Record
}

func (p scheduleTestProvider) ID() string            { return "test" }
func (p scheduleTestProvider) BackendName() string   { return "test" }
func (p scheduleTestProvider) CacheIdentity() string { return "test" }
func (p scheduleTestProvider) Search(_ context.Context, _ hosts.Query) ([]hosts.Record, error) {
	return []hosts.Record{p.rec}, nil
}

type scheduleTestFactory struct {
	rec hosts.Record
}

func (f scheduleTestFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	return []hosts.Backend{scheduleTestProvider{rec: f.rec}}
}
func (f scheduleTestFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return scheduleTestProvider{rec: f.rec}
}
func (f scheduleTestFactory) BackendRows() []config.BackendRow { return nil }

func TestRecipeE2E_Schedule(t *testing.T) {
	// 1. Setup real SSH container
	sshH, sshP, keyFile := startSSH(t)

	// Build the target record for our test SSH container
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

	// 2. Setup mock search registry that returns our SSH container
	searchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{scheduleTestFactory{rec: rec}})

	// 3. Setup mock exec registry that uses real SSH dialing to our container
	execReg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	// 4. Create the recipe file
	tmpDir := t.TempDir()
	recipePath := filepath.Join(tmpDir, "schedule.cue")
	cueContent := `
recipe: {
	name: "test-schedule"
	schedules: {
		"every-second": {
			// gronx supports 6-part cron for seconds. * * * * * * = every second.
			cron: "* * * * * *"
			env: {
				SCHED_VAR: "hello-from-e2e-schedule"
			}
		}
	}
	steps: [
		{
			host: "*"
			env: {
				SCHED_VAR: string | *""
			}
			command: "echo \(env.SCHED_VAR) >> /tmp/schedule_out.txt"
		}
	]
}
`
	require.NoError(t, os.WriteFile(recipePath, []byte(cueContent), 0o600))

	// 5. Create config
	configPath := filepath.Join(tmpDir, "honey.yaml")
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{
			"schedule_app": {
				Type:         apps.AppTypeRecipe,
				TargetRecipe: recipePath,
				Target:       "ssh-test",
			},
		},
		Defaults: config.Defaults{
			SSHUser: "testuser",
		},
	}
	require.NoError(t, os.WriteFile(configPath, []byte(""), 0o600))

	// 6. Setup and start Webserver
	// newTestServer internally sets up the scheduleManager and starts it.
	opts := webserver.Options{
		ConfigPath:     configPath,
		Config:         cfg,
		Token:          "test-token",
		SearchRegistry: searchReg,
		ExecRegistry:   execReg,
	}

	// Start server. It will run the schedules in the background.
	_ = newTestServer(t, opts)

	// 7. Wait for cron to trigger at least once (gronx triggers exactly on the second mark)
	time.Sleep(3 * time.Second)

	// 8. Verify on SSH container
	client, err := execReg.Dialer.Dial("testuser", sshH, sshP, keyFile)
	require.NoError(t, err)
	defer client.Close()

	output, err := client.Run("cat /tmp/schedule_out.txt")
	require.NoError(t, err)
	
	// We expect at least one line of output.
	assert.Contains(t, string(output), "hello-from-e2e-schedule\n")
}
