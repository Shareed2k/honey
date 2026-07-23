package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/distribution/reference"
	"gopkg.in/yaml.v3"

	"github.com/shareed2k/honey/internal/safepath"
)

// Capability names registered in plugin.yaml.
const (
	CapCueTransform = "cue_transform"
	CapCustomStep   = "custom_step"
	CapSecret       = "secret"
	CapStackUnwrap  = "stack_unwrap"
	CapHook         = "hook"
)

// Manifest describes a plugin bundle (plugin.yaml next to plugin.wasm).
type Manifest struct {
	ID                   string            `yaml:"id"`
	Version              string            `yaml:"version"`
	Capabilities         []string          `yaml:"capabilities"`
	SecretRefPrefixes    []string          `yaml:"secret_ref_prefixes,omitempty"`
	AllowK8sHTTP         bool              `yaml:"allow_k8s_http,omitempty"`
	AllowHostExec        bool              `yaml:"allow_host_exec,omitempty"`
	AllowRemoteExec      bool              `yaml:"allow_remote_exec,omitempty"`
	AllowSFTP            bool              `yaml:"allow_sftp,omitempty"`
	AllowTemplateRender  bool              `yaml:"allow_template_render,omitempty"`
	AllowPostgres        bool              `yaml:"allow_postgres,omitempty"`
	AllowKV              bool              `yaml:"allow_kv,omitempty"`
	AllowedEnv           []string          `yaml:"allowed_env,omitempty"`
	AllowedHosts         []string          `yaml:"allowed_hosts,omitempty"`
	AllowedPaths         map[string]string `yaml:"allowed_paths,omitempty"`
	Config               map[string]string `yaml:"config,omitempty"`
	MaxHTTPResponseBytes int64             `yaml:"max_http_response_bytes,omitempty"`
	Order                int               `yaml:"order,omitempty"`
	Runtime              string            `yaml:"runtime,omitempty"` // "wasm" (default) or "docker"
	Docker               *DockerRuntime    `yaml:"docker,omitempty"`
}

func (m Manifest) hasCapability(name string) bool {
	for _, c := range m.Capabilities {
		if strings.TrimSpace(c) == name {
			return true
		}
	}
	return false
}

// DockerRuntime configures a runtime: docker plugin's container.
type DockerRuntime struct {
	Image      string              `yaml:"image"`
	PullPolicy string              `yaml:"pull_policy,omitempty"` // "if_not_present" (default) or "always"
	Restart    DockerRestartConfig `yaml:"restart,omitempty"`
	// Volumes are static bind mounts, Docker syntax ("host_path:container_path[:ro|rw]"),
	// same format container.HostConfig.Binds already takes. For plugins that need file
	// I/O (e.g. ffmpeg reading/writing files) under one predictable host root — not
	// per-call dynamic mounts.
	Volumes []string `yaml:"volumes,omitempty"`
	// Init selects who provides honey-plugin-init: "" or "bind" (default) —
	// honey bind-mounts the host binary and overrides the entrypoint; "embedded"
	// — the image already contains honey-plugin-init as its entrypoint, so no
	// host binary is needed (required for images distributed from a registry).
	Init string `yaml:"init,omitempty"`
	// InitPath is the in-image path of honey-plugin-init when Init=="embedded".
	// Defaults to defaultEmbeddedInitPath at load. Ignored in bind mode.
	InitPath string `yaml:"init_path,omitempty"`
}

// DockerRestartConfig tunes auto-restart of a crashed plugin container.
type DockerRestartConfig struct {
	MaxBackoff string `yaml:"max_backoff,omitempty"` // duration string, default "30s"
}

// effectiveRuntime returns the plugin's runtime, defaulting to "wasm".
func (m Manifest) effectiveRuntime() string {
	r := strings.TrimSpace(m.Runtime)
	if r == "" {
		return "wasm"
	}
	return r
}

// effectivePullPolicy defaults to "if_not_present".
func (d DockerRuntime) effectivePullPolicy() string {
	p := strings.TrimSpace(d.PullPolicy)
	if p == "" {
		return "if_not_present"
	}
	return p
}

// effectiveMaxBackoff defaults to 30s.
func (d DockerRuntime) effectiveMaxBackoff() (time.Duration, error) {
	s := strings.TrimSpace(d.Restart.MaxBackoff)
	if s == "" {
		return 30 * time.Second, nil
	}
	return time.ParseDuration(s)
}

// defaultEmbeddedInitPath is where honey expects honey-plugin-init inside an
// embedded-init image unless docker.init_path overrides it.
const defaultEmbeddedInitPath = "/usr/local/bin/honey-plugin-init"

// effectiveInitMode returns "bind" or "embedded"; empty defaults to "bind".
func (d DockerRuntime) effectiveInitMode() string {
	m := strings.TrimSpace(d.Init)
	if m == "" {
		return "bind"
	}
	return m
}

// expandVolumeHostPath expands a leading "~" (bare or "~/...") in a
// docker.volumes bind spec's host-path segment to the current user's home
// directory. Docker's own API has no shell to do this — passed through
// literally, "~/..." fails validation as an invalid *named volume* rather
// than the host directory the manifest author meant.
func expandVolumeHostPath(spec string) (string, error) {
	hostPath, rest, ok := strings.Cut(spec, ":")
	if !ok {
		return spec, nil // malformed; isValidBindSpec reports it
	}
	if hostPath == "~" || strings.HasPrefix(hostPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~ in volume host path: %w", err)
		}
		hostPath = home + strings.TrimPrefix(hostPath, "~")
	}
	return hostPath + ":" + rest, nil
}

func isValidBindSpec(v string) bool {
	parts := strings.Split(v, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	if strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return false
	}
	if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
		return false
	}
	return true
}

// imageIsDigestPinned reports whether image carries an @sha256:... digest,
// parsed properly (not substring-matched) so a tag containing the substring
// cannot pass. A canonical reference is Named + Digested.
func imageIsDigestPinned(image string) bool {
	ref, err := reference.ParseNormalizedNamed(strings.TrimSpace(image))
	if err != nil {
		return false
	}
	_, ok := ref.(reference.Canonical)
	return ok
}

func loadManifest(path string) (Manifest, error) {
	var m Manifest
	b, err := safepath.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	m.ID = strings.TrimSpace(m.ID)
	if m.ID == "" {
		return m, fmt.Errorf("manifest %s: id is required", path)
	}
	if len(m.Capabilities) == 0 {
		return m, fmt.Errorf("manifest %s: capabilities is required", path)
	}
	if m.effectiveRuntime() == "docker" {
		if m.Docker == nil || strings.TrimSpace(m.Docker.Image) == "" {
			return m, fmt.Errorf("manifest %s: runtime: docker requires docker.image", path)
		}
		switch m.Docker.effectiveInitMode() {
		case "bind":
			// current behavior; no extra requirements
		case "embedded":
			if !imageIsDigestPinned(m.Docker.Image) {
				return m, fmt.Errorf("manifest %s: docker.init: embedded requires a digest-pinned image (…@sha256:…), got %q", path, m.Docker.Image)
			}
			if strings.TrimSpace(m.Docker.InitPath) == "" {
				m.Docker.InitPath = defaultEmbeddedInitPath
			}
		default:
			return m, fmt.Errorf("manifest %s: docker.init must be \"bind\" or \"embedded\", got %q", path, m.Docker.Init)
		}
		for i, v := range m.Docker.Volumes {
			expanded, err := expandVolumeHostPath(v)
			if err != nil {
				return m, fmt.Errorf("manifest %s: docker.volumes: %w", path, err)
			}
			m.Docker.Volumes[i] = expanded
			if !isValidBindSpec(expanded) {
				return m, fmt.Errorf("manifest %s: invalid docker.volumes entry %q (want \"host_path:container_path[:ro|rw]\")", path, expanded)
			}
		}
	}
	return m, nil
}

// discoverPluginDirs finds candidate plugin directories under root: any
// immediate subdirectory containing a plugin.yaml. It does not require
// plugin.wasm — runtime: docker plugins have no wasm module, only a
// plugin.yaml + plugin.cue. Runtime-specific required files (plugin.wasm for
// wasm, plugin.cue for docker) are validated later in loadPluginDir's
// dispatch, where a missing file surfaces as a clear load error instead of a
// silent skip.
func discoverPluginDirs(root string) ([]string, error) {
	entries, err := safepath.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		manifestPath := filepath.Join(dir, "plugin.yaml")
		if st, err := safepath.Stat(manifestPath); err == nil && !st.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}
