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

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/plugins"
)

// loadTestCVEScannerManager builds the plugin manager pointing to the cve-scanner wasm
func loadTestCVEScannerManager(t *testing.T) *plugins.Manager {
	t.Helper()
	cwd, err := os.Getwd()
	require.NoError(t, err)

	// find project root
	root := cwd
	for d := cwd; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			root = d
			break
		}
	}

	src := filepath.Join(root, "plugins", "cve-scanner")
	wasm := filepath.Join(src, "plugin.wasm")
	if _, err := os.Stat(wasm); err != nil {
		t.Skipf("plugin.wasm not built: %v", err)
	}

	dir := t.TempDir()
	dst := filepath.Join(dir, "cve-scanner")
	require.NoError(t, os.MkdirAll(dst, 0o755))

	for _, f := range []string{"plugin.yaml", "plugin.wasm"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dst, f), b, 0o644))
	}

	cfg := config.PluginsEffective{
		Enabled:     true,
		Directory:   dir,
		MaxMemoryMB: 64,
		TimeoutMS:   300000,
	}
	mgr, err := plugins.NewManager(context.Background(), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })

	return mgr
}

func TestRecipeE2E_PluginCVEScanner(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)
	
	// Create a fake trivial vulnerability database mock or run a dummy command
	// For E2E we'll just run auto installer that fails download or does a dry-run style command.
	// Since we are running in an alpine-based SSH container, we can run "scan" and see it inject the logic
	// The SSH container used in tests/integration/containers.go is typically alpine.
	
	cueContent := `
recipe: {
	name: "test-cve-scanner"
	type: "graph"
	steps: [
		{
			id: "scan"
			host: "*"
			plugin: {
				id: "cve-scanner"
				action: "scan"
				config: {
					scanner: "auto"
					target: "dir:/"
				}
			}
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)

	params := engine.CueRecipeRunParams{
		Recipe:    recipe,
		Records:   []hosts.Record{rec},
		SSHUser:   "testuser",
		Execute:   true,
		Reg:       reg,
		PluginMgr: loadTestCVEScannerManager(t),
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
	
	t.Logf("OUTPUT: %q", results[0].Output)
	assert.True(t, results[0].Success)
	assert.Contains(t, results[0].Output, `"scanner":"trivy"`)
	assert.Contains(t, results[0].Output, `"total":0`)
}
