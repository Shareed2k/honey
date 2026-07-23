package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	const yaml = `id: echo
version: "0.1.0"
capabilities:
  - cue_transform
  - custom_step
secret_ref_prefixes:
  - "echo:"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "echo" {
		t.Fatalf("id=%q", m.ID)
	}
	if !m.hasCapability(CapCueTransform) {
		t.Fatal("expected cue_transform capability")
	}
	if len(m.SecretRefPrefixes) != 1 || m.SecretRefPrefixes[0] != "echo:" {
		t.Fatalf("prefixes=%v", m.SecretRefPrefixes)
	}
}

func TestLoadManifest_DockerRuntime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	const yaml = `id: trivy
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "aquasec/trivy:0.72.0"
  pull_policy: always
  restart:
    max_backoff: 45s
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.effectiveRuntime() != "docker" {
		t.Fatalf("runtime=%q", m.effectiveRuntime())
	}
	if m.Docker == nil || m.Docker.Image != "aquasec/trivy:0.72.0" {
		t.Fatalf("docker=%+v", m.Docker)
	}
	if m.Docker.effectivePullPolicy() != "always" {
		t.Fatalf("pull_policy=%q", m.Docker.effectivePullPolicy())
	}
	backoff, err := m.Docker.effectiveMaxBackoff()
	if err != nil || backoff != 45*time.Second {
		t.Fatalf("max_backoff=%v err=%v", backoff, err)
	}
}

func TestLoadManifest_DefaultRuntimeIsWasm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(path, []byte("id: echo\nversion: \"0.1.0\"\ncapabilities:\n  - cue_transform\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.effectiveRuntime() != "wasm" {
		t.Fatalf("runtime=%q want wasm default", m.effectiveRuntime())
	}
}

func TestLoadManifest_DockerRuntimeDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	const yaml = `id: trivy
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "aquasec/trivy:0.72.0"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Docker.effectivePullPolicy() != "if_not_present" {
		t.Fatalf("pull_policy=%q want if_not_present default", m.Docker.effectivePullPolicy())
	}
	backoff, err := m.Docker.effectiveMaxBackoff()
	if err != nil || backoff != 30*time.Second {
		t.Fatalf("max_backoff=%v err=%v want 30s default", backoff, err)
	}
}

func TestLoadManifest_DockerRuntimeMissingImageFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	const yaml = `id: trivy
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(path); err == nil {
		t.Fatal("expected error for runtime: docker with no docker.image")
	}
}

func TestLoadManifest_DockerRuntimeVolumes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	const yaml = `id: ffmpeg
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "linuxserver/ffmpeg:latest"
  volumes:
    - "/var/honey/media:/data:rw"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Docker.Volumes) != 1 || m.Docker.Volumes[0] != "/var/honey/media:/data:rw" {
		t.Fatalf("volumes=%v", m.Docker.Volumes)
	}
}

func TestLoadManifest_DockerRuntimeInvalidVolumeFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.yaml")
	const yaml = `id: ffmpeg
version: "0.1.0"
capabilities:
  - custom_step
runtime: docker
docker:
  image: "linuxserver/ffmpeg:latest"
  volumes:
    - "not-a-valid-bind-spec"
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadManifest(path); err == nil {
		t.Fatal("expected error for a volume entry missing host:container syntax")
	}
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "plugin.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadManifest_InitDefaultsToBind(t *testing.T) {
	m, err := loadManifest(writeManifest(t, `
id: p
capabilities: [custom_step]
runtime: docker
docker:
  image: "img:tag"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := m.Docker.effectiveInitMode(); got != "bind" {
		t.Errorf("effectiveInitMode = %q, want bind", got)
	}
}

func TestLoadManifest_InitInvalidRejected(t *testing.T) {
	_, err := loadManifest(writeManifest(t, `
id: p
capabilities: [custom_step]
runtime: docker
docker:
  image: "img:tag"
  init: sideload
`))
	if err == nil || !strings.Contains(err.Error(), `docker.init`) {
		t.Fatalf("want docker.init error, got %v", err)
	}
}

func TestLoadManifest_EmbeddedRequiresDigest(t *testing.T) {
	_, err := loadManifest(writeManifest(t, `
id: p
capabilities: [custom_step]
runtime: docker
docker:
  image: "ghcr.io/org/img:v1"
  init: embedded
`))
	if err == nil || !strings.Contains(err.Error(), "digest-pinned") {
		t.Fatalf("want digest-pinned error, got %v", err)
	}
}

func TestLoadManifest_EmbeddedWithDigestOKAndDefaultsInitPath(t *testing.T) {
	digest := "ghcr.io/org/img@sha256:" + strings.Repeat("a", 64)
	m, err := loadManifest(writeManifest(t, `
id: p
capabilities: [custom_step]
runtime: docker
docker:
  image: "`+digest+`"
  init: embedded
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Docker.effectiveInitMode() != "embedded" {
		t.Errorf("mode = %q, want embedded", m.Docker.effectiveInitMode())
	}
	if m.Docker.InitPath != defaultEmbeddedInitPath {
		t.Errorf("InitPath = %q, want default %q", m.Docker.InitPath, defaultEmbeddedInitPath)
	}
}

func TestLoadManifest_EmbeddedRespectsExplicitInitPath(t *testing.T) {
	digest := "ghcr.io/org/img@sha256:" + strings.Repeat("b", 64)
	m, err := loadManifest(writeManifest(t, `
id: p
capabilities: [custom_step]
runtime: docker
docker:
  image: "`+digest+`"
  init: embedded
  init_path: "/opt/honey-plugin-init"
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Docker.InitPath != "/opt/honey-plugin-init" {
		t.Errorf("InitPath = %q, want /opt/honey-plugin-init", m.Docker.InitPath)
	}
}

func TestManagerDisabled(t *testing.T) {
	m, err := NewManager(t.Context(), PluginsFromConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	if m.Enabled() {
		t.Fatal("expected disabled")
	}
	if len(m.List()) != 0 {
		t.Fatalf("list=%v", m.List())
	}
	out, err := m.TransformCue(t.Context(), []byte("package x\n"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "package x\n" {
		t.Fatalf("transform changed bytes: %q", out)
	}
}
