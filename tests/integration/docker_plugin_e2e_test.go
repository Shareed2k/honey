//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/plugins"
)

// buildPluginInitForTest compiles cmd/honey-plugin-init for Linux (matching
// what this test's Docker daemon runs containers as, even when the host
// itself isn't Linux) at the host's own architecture, into a temp dir, and
// points HONEY_PLUGIN_INIT_PATH at it.
//
// The build output deliberately lives under the repo root rather than
// t.TempDir(): on a non-Linux host, Docker runs inside a VM (Docker Desktop,
// Colima, Lima, ...) whose filesystem sharing is commonly scoped to the
// user's home directory, not the OS temp dir (e.g. macOS's os.TempDir() is
// under /var/folders, outside a Colima VM's virtiofs share of $HOME). A
// binary bind-mounted from an unshared host path resolves to nothing inside
// the VM, and Docker silently materializes an empty directory at the mount
// target instead of erroring — surfacing later as a confusing "is a
// directory" or I/O error when the container tries to exec it. Building
// under the repo (itself under $HOME in the common case) keeps the binary
// inside whatever's shared.
func buildPluginInitForTest(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp(repoRoot(t), "honey-plugin-init-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	out := filepath.Join(dir, "honey-plugin-init")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/honey-plugin-init")
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "GOOS=linux")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build honey-plugin-init: %v\n%s", err, output)
	}
	t.Setenv("HONEY_PLUGIN_INIT_PATH", out)
}

// repoRoot assumes tests run from tests/integration/ (matches this package's
// existing convention for locating repo-relative paths).
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..")
}

// writeDockerPlugin writes a runtime: docker plugin bundle and returns the
// *root* plugins directory (not the plugin's own directory): LoadFromDir /
// NewManager discover plugins by scanning root for immediate subdirectories
// that contain a plugin.yaml (see discoverPluginDirs and the existing
// "testdata/echo/plugin.yaml" WASM-plugin convention in this package), so
// the manifest must live one level below what gets passed to LoadFromDir.
func writeDockerPlugin(t *testing.T, image string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "echo-docker")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf(`id: echo-docker
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "%s"
  restart:
    max_backoff: 2s
`, image)
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	const cue = `
actions: echo: {
	#Config: { message: string }
	argv: ["echo", "-n", config.message]
	output_format: "text"
}
actions: fail: {
	#Config: {}
	argv: ["sh", "-c", "exit 7"]
	output_format: "text"
}
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.cue"), []byte(cue), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDockerPlugin_LoadAndCall(t *testing.T) {
	buildPluginInitForTest(t)
	dir := writeDockerPlugin(t, "busybox:latest")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mgr, err := plugins.LoadFromDir(ctx, dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	defer mgr.Close()

	var out struct {
		Output string `json:"output"`
	}
	if err := mgr.Call(ctx, "echo-docker", "echo", map[string]any{"message": "hello-from-docker-plugin"}, &out); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out.Output != "hello-from-docker-plugin" {
		t.Fatalf("output=%q want hello-from-docker-plugin", out.Output)
	}
}

func TestDockerPlugin_NonZeroExitSurfacesAsError(t *testing.T) {
	buildPluginInitForTest(t)
	dir := writeDockerPlugin(t, "busybox:latest")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mgr, err := plugins.LoadFromDir(ctx, dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	defer mgr.Close()

	err = mgr.Call(ctx, "echo-docker", "fail", map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error from an action that exits nonzero")
	}
}
