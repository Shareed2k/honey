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

	// Scan an empty, freshly created directory rather than the container's
	// real rootfs: trivy only reports findings for packages/manifests it can
	// detect, so an empty dir yields total:0 deterministically, independent
	// of what the live vulnerability database currently knows about the
	// container's actual installed OS packages (which changes over time as
	// new CVEs are disclosed against already-shipped packages).
	cueContent := `
recipe: {
	name: "test-cve-scanner"
	type: "linear"
	steps: [
		{
			host: "*"
			command: "mkdir -p /tmp/cve-scan-empty"
		},
		{
			host: "*"
			plugin: {
				id: "cve-scanner"
				action: "scan"
				config: {
					scanner: "auto"
					target: "dir:/tmp/cve-scan-empty"
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

	require.Len(t, results, 2)

	mkdirResult, scanResult := results[0], results[1]
	require.True(t, mkdirResult.Success, "mkdir step failed: %s", mkdirResult.Output)

	t.Logf("OUTPUT: %q", scanResult.Output)
	assert.True(t, scanResult.Success)
	assert.Contains(t, scanResult.Output, `"scanner":"trivy"`)
	assert.Contains(t, scanResult.Output, `"total":0`)
}
