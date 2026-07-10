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
	// CGO_ENABLED=0 is forced explicitly, not left to Go's own
	// cross-compile default: Taskfile.yml sets a global CGO_ENABLED=1 for
	// every task (other tasks need it), which leaks into this process's
	// inherited os.Environ() when run via `task test:integration` and
	// forces cgo on for this GOOS=linux cross-compile — failing on macOS,
	// which has no Linux cgo toolchain (runtime/cgo's own Linux-specific
	// shims like setresgid/clearenv don't exist in the macOS SDK headers).
	// Plain `go test` never hits this because nothing sets CGO_ENABLED
	// there, so Go's cross-compile default (0) applies on its own.
	cmd.Env = append(os.Environ(), "GOOS=linux", "CGO_ENABLED=0")
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

// writeFfmpegPlugin writes a runtime: docker plugin bundle whose two actions
// (generate, probe) exec ffmpeg/ffprobe directly inside a linuxserver/ffmpeg
// container, with hostVolumeDir bind-mounted at /data so a file written by
// one action's argv is visible to another action's argv in a later call.
//
// argv[0] for each action is the ffmpeg/ffprobe binary's absolute path
// (/usr/local/bin/ffmpeg, /usr/local/bin/ffprobe), not "ffmpeg"/"ffprobe":
// createAndStart (docker_transport.go) always overrides the container's
// Entrypoint with honey-plugin-init, and honey-plugin-init execs argv[0]
// directly (see cmd/honey-plugin-init/main.go's runArgv) rather than through
// the image's own entrypoint/shell, so the image's default ffmpeg-wrapping
// ENTRYPOINT (/ffmpegwrapper.sh, confirmed via `docker inspect`) never runs.
func writeFfmpegPlugin(t *testing.T, hostVolumeDir string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "ffmpeg-e2e")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := fmt.Sprintf(`id: ffmpeg-e2e
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "linuxserver/ffmpeg:latest"
  pull_policy: if_not_present
  volumes:
    - "%s:/data:rw"
`, hostVolumeDir)
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	const cue = `
actions: generate: {
	#Config: { output: string }
	argv: ["/usr/local/bin/ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=blue:size=64x64:d=1", config.output]
	output_format: "text"
}
actions: probe: {
	#Config: { input: string }
	argv: ["/usr/local/bin/ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", config.input]
	output_format: "json"
}
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.cue"), []byte(cue), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestDockerPlugin_FfmpegGenerateAndProbe proves a real, non-trivial
// multi-action docker-runtime plugin works end-to-end: one action generates a
// tiny synthetic video clip with ffmpeg, a second action inspects that same
// file with ffprobe, and docker.volumes is what makes the file written by
// the first call visible to the second — both calls land on the same
// long-lived plugin container (dockerTransport is created once per
// LoadFromDir and reused across Manager.Call invocations), so the bind mount
// only needs to be consistent within that one container's view of /data, not
// necessarily visible back on the test-runner host (on a VM-backed Docker
// setup — Docker Desktop/Colima/Lima — a t.TempDir() host path outside the
// VM's shared mount range is silently backed by a VM-local directory instead
// of erroring; that's fine here since both actions only ever read/write
// through the container's own /data view of it).
func TestDockerPlugin_FfmpegGenerateAndProbe(t *testing.T) {
	buildPluginInitForTest(t)
	hostVolumeDir := t.TempDir()
	dir := writeFfmpegPlugin(t, hostVolumeDir)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	mgr, err := plugins.LoadFromDir(ctx, dir)
	if err != nil {
		t.Fatalf("LoadFromDir: %v", err)
	}
	defer mgr.Close()

	if err := mgr.Call(ctx, "ffmpeg-e2e", "generate", map[string]any{"output": "/data/clip.mp4"}, nil); err != nil {
		t.Fatalf("Call generate: %v", err)
	}

	var probe struct {
		Format struct {
			Filename string `json:"filename"`
			Duration string `json:"duration"`
			Size     string `json:"size"`
		} `json:"format"`
	}
	if err := mgr.Call(ctx, "ffmpeg-e2e", "probe", map[string]any{"input": "/data/clip.mp4"}, &probe); err != nil {
		t.Fatalf("Call probe: %v", err)
	}

	if probe.Format.Duration == "" {
		t.Fatalf("ffprobe output missing format.duration, got %+v", probe)
	}
	if probe.Format.Filename != "/data/clip.mp4" {
		t.Fatalf("format.filename = %q, want /data/clip.mp4", probe.Format.Filename)
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
