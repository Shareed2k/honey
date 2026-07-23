package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	extism "github.com/extism/go-sdk"

	"github.com/shareed2k/honey/internal/config"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/safepath"
)

// Info describes a loaded plugin for listing.
type Info struct {
	ID                   string            `json:"id"`
	Version              string            `json:"version"`
	Capabilities         []string          `json:"capabilities"`
	Path                 string            `json:"path"`
	SecretRefPrefixes    []string          `json:"secret_ref_prefixes,omitempty"`
	AllowHostExec        bool              `json:"allow_host_exec,omitempty"`
	AllowRemoteExec      bool              `json:"allow_remote_exec,omitempty"`
	AllowSFTP            bool              `json:"allow_sftp,omitempty"`
	AllowTemplateRender  bool              `json:"allow_template_render,omitempty"`
	AllowPostgres        bool              `json:"allow_postgres,omitempty"`
	AllowKV              bool              `json:"allow_kv,omitempty"`
	AllowedHosts         []string          `json:"allowed_hosts,omitempty"`
	AllowedPaths         map[string]string `json:"allowed_paths,omitempty"`
	AllowedEnv           []string          `json:"allowed_env,omitempty"`
	MaxHTTPResponseBytes int64             `json:"max_http_response_bytes,omitempty"`
}

// Manager loads Extism WASM plugins and routes capability calls.
type Manager struct {
	mu        sync.Mutex
	enabled   bool
	timeoutMS int
	plugins   []*loadedPlugin
	byID      map[string]*loadedPlugin
}

type loadedPlugin struct {
	manifest       Manifest
	effectiveHosts []string
	effectivePaths map[string]string
	dir            string
	wasm           []byte
	cueSource      []byte // plugin.cue bytes, retained for docker plugins so a
	// DockerHostSession can build a fresh per-remote-host transport
	transport pluginTransport
	callMu    sync.Mutex // neither extism.Plugin nor dockerTransport is safe for concurrent calls
}

func clonePathMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// NewManager loads plugins from cfg. When plugins are disabled, returns a manager with no plugins.
func NewManager(ctx context.Context, cfg config.PluginsEffective) (*Manager, error) {
	m := &Manager{
		enabled:   cfg.Enabled,
		timeoutMS: cfg.TimeoutMS,
		byID:      make(map[string]*loadedPlugin),
	}
	if !cfg.Enabled {
		return m, nil
	}
	dirs, err := discoverPluginDirs(cfg.Directory)
	if err != nil {
		return nil, fmt.Errorf("plugins: scan %s: %w", cfg.Directory, err)
	}
	allow := make(map[string]struct{}, len(cfg.Allowlist))
	for _, id := range cfg.Allowlist {
		id = strings.TrimSpace(id)
		if id != "" {
			allow[id] = struct{}{}
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i] < dirs[j] })
	for _, dir := range dirs {
		lp, err := loadPluginDir(ctx, dir, cfg)
		if err != nil {
			return nil, err
		}
		if len(allow) > 0 {
			if _, ok := allow[lp.manifest.ID]; !ok {
				continue
			}
		}
		if _, dup := m.byID[lp.manifest.ID]; dup {
			return nil, fmt.Errorf("plugins: duplicate plugin id %q", lp.manifest.ID)
		}
		m.plugins = append(m.plugins, lp)
		m.byID[lp.manifest.ID] = lp
	}
	sort.Slice(m.plugins, func(i, j int) bool {
		if m.plugins[i].manifest.Order != m.plugins[j].manifest.Order {
			return m.plugins[i].manifest.Order < m.plugins[j].manifest.Order
		}
		return m.plugins[i].manifest.ID < m.plugins[j].manifest.ID
	})
	return m, nil
}

func loadPluginDir(ctx context.Context, dir string, cfg config.PluginsEffective) (*loadedPlugin, error) {
	manifestPath := filepath.Join(dir, "plugin.yaml")
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	hosts, paths, err := validateManifestPolicy(manifest, cfg)
	if err != nil {
		return nil, err
	}
	if manifest.effectiveRuntime() == "docker" {
		return loadDockerPluginDir(ctx, dir, manifest, hosts, paths)
	}
	return loadWasmPluginDir(ctx, dir, manifest, hosts, paths, cfg)
}

// loadWasmPluginDir is today's plugin-loading body, unchanged except for
// taking manifest/hosts/paths as params (already computed by loadPluginDir)
// instead of recomputing them.
func loadWasmPluginDir(ctx context.Context, dir string, manifest Manifest, hosts []string, paths map[string]string, cfg config.PluginsEffective) (*loadedPlugin, error) {
	wasmPath := filepath.Join(dir, "plugin.wasm")
	wasm, err := safepath.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("plugins: read %s: %w", wasmPath, err)
	}
	manifestCfg := map[string]string{
		"plugin_id": manifest.ID,
	}
	for k, v := range manifest.Config {
		manifestCfg[k] = v
	}
	timeout, err := extismTimeoutMS(cfg.TimeoutMS)
	if err != nil {
		return nil, err
	}
	man := extism.Manifest{
		Wasm: []extism.Wasm{
			extism.WasmData{Data: wasm},
		},
		Config:       manifestCfg,
		Timeout:      timeout,
		AllowedHosts: hosts,
		AllowedPaths: paths,
	}
	if cfg.MaxMemoryMB > 0 {
		pages, err := extismMemoryPages(cfg.MaxMemoryMB)
		if err != nil {
			return nil, err
		}
		mem := &extism.ManifestMemory{MaxPages: pages}
		if manifest.MaxHTTPResponseBytes > 0 {
			mem.MaxHttpResponseBytes = manifest.MaxHTTPResponseBytes
		} else {
			mem.MaxHttpResponseBytes = 4 << 20
		}
		man.Memory = mem
	}
	plug, err := extism.NewPlugin(ctx, man, extism.PluginConfig{EnableWasi: true}, hostFunctions(manifest, cfg.TimeoutMS))
	if err != nil {
		return nil, fmt.Errorf("plugins: instantiate %q: %w", manifest.ID, err)
	}
	return &loadedPlugin{
		manifest:       manifest,
		effectiveHosts: hosts,
		effectivePaths: paths,
		dir:            dir,
		wasm:           wasm,
		transport:      &extismTransport{plugin: plug},
	}, nil
}

// loadDockerPluginDir loads a runtime: docker plugin: reads its plugin.cue,
// resolves the docker.init mode, and starts the container. In bind mode
// (default) it also locates the host honey-plugin-init binary to bind-mount
// as the container entrypoint; in embedded mode the image supplies its own
// init at manifest.Docker.InitPath, so no host binary is located or bound.
func loadDockerPluginDir(ctx context.Context, dir string, manifest Manifest, hosts []string, paths map[string]string) (*loadedPlugin, error) {
	cuePath := filepath.Join(dir, "plugin.cue")
	cueBytes, err := safepath.ReadFile(cuePath)
	if err != nil {
		return nil, fmt.Errorf("plugins: read %s: %w", cuePath, err)
	}
	initMode := manifest.Docker.effectiveInitMode()

	var initPath string
	if initMode == "bind" {
		p, err := locatePluginInitBinary()
		if err != nil {
			return nil, err
		}
		initPath = p
	}
	// embedded mode: no host shim binary; initPath stays "".

	// Ensure the global plugin workspaces directory exists and mount it
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("plugins: get home dir for workspaces: %w", err)
	}
	workspacesDir := filepath.Join(homeDir, ".honey", "workspaces")
	if err := safepath.MkdirAll(workspacesDir, 0o755); err != nil {
		return nil, fmt.Errorf("plugins: create workspaces dir %s: %w", workspacesDir, err)
	}

	volumes := manifest.Docker.Volumes
	volumes = append(volumes, fmt.Sprintf("%s:%s:rw", workspacesDir, workspacesDir))

	maxBackoff, err := manifest.Docker.effectiveMaxBackoff()
	if err != nil {
		return nil, fmt.Errorf("plugins: invalid docker.restart.max_backoff: %w", err)
	}
	backend, err := newLocalBackend(initPath, "")
	if err != nil {
		return nil, fmt.Errorf("plugins: instantiate docker plugin %q: %w", manifest.ID, err)
	}
	dt, err := newDockerTransport(ctx, backend, dockerTransportConfig{
		Image:      manifest.Docker.Image,
		PullPolicy: manifest.Docker.effectivePullPolicy(),
		CueSource:  cueBytes,
		MaxBackoff: maxBackoff,
		Env:        resolveAllowedEnv(manifest.AllowedEnv),
		Volumes:    volumes,
		InitMode:   initMode,
		InitPath:   manifest.Docker.InitPath,
	})
	if err != nil {
		return nil, fmt.Errorf("plugins: instantiate docker plugin %q: %w", manifest.ID, err)
	}
	return &loadedPlugin{
		manifest:       manifest,
		effectiveHosts: hosts,
		effectivePaths: paths,
		dir:            dir,
		cueSource:      cueBytes,
		transport:      dt,
	}, nil
}

// resolveAllowedEnv resolves each allowed name from honey's own process
// environment, for passthrough into a docker plugin's container — the same
// "allowed_env" manifest field WASM plugins use for the get_env host
// function, reinterpreted here since docker plugins have no mediated
// host-function call to gate (see spec's capability model). Names with no
// value set in honey's environment are silently omitted, not errored.
func resolveAllowedEnv(names []string) map[string]string {
	env := make(map[string]string, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if v, ok := os.LookupEnv(name); ok {
			env[name] = v
		}
	}
	return env
}

// locatePluginInitBinary finds the honey-plugin-init binary to bind-mount
// into docker-runtime plugin containers: HONEY_PLUGIN_INIT_PATH env var if
// set (also how tests_test.go/tests/integration point at a freshly-built
// binary); otherwise alongside the running honey executable, preferring an
// arch-suffixed binary (honey-plugin-init-linux-$GOARCH — how release
// archives and the Homebrew formula ship it, one per target container
// architecture) over the plain unsuffixed name (hand-built via `task
// build-honey-plugin-init`, or HONEY_PLUGIN_INIT_PATH-adjacent setups).
func locatePluginInitBinary() (string, error) {
	if p := strings.TrimSpace(os.Getenv("HONEY_PLUGIN_INIT_PATH")); p != "" {
		if err := validatePluginInitBinaryPath(p); err != nil {
			return "", fmt.Errorf("plugins: HONEY_PLUGIN_INIT_PATH=%s: %w", p, err)
		}
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("plugins: locate honey-plugin-init: %w", err)
	}
	dir := filepath.Dir(exe)
	archPath := filepath.Join(dir, "honey-plugin-init-linux-"+runtime.GOARCH)
	if validatePluginInitBinaryPath(archPath) == nil {
		return archPath, nil
	}
	path := filepath.Join(dir, "honey-plugin-init")
	if err := validatePluginInitBinaryPath(path); err != nil {
		return "", fmt.Errorf("plugins: honey-plugin-init not found at %s or %s (build it via `task build-honey-plugin-init` or set HONEY_PLUGIN_INIT_PATH): %w", archPath, path, err)
	}
	return path, nil
}

// LocateShimBinaryForArch finds the honey-plugin-init binary built for the
// given GOARCH ("amd64"/"arm64"), for staging onto a remote host of that
// architecture (the remote docker-plugin path). Searches, in order, for
// "honey-plugin-init-linux-<goarch>" in: $HONEY_PLUGIN_INIT_DIR, the directory
// of $HONEY_PLUGIN_INIT_PATH (release archives ship both arches side by side,
// so the arch-suffixed sibling of the operator's own binary is the natural
// spot), then alongside the running honey executable. Unlike
// locatePluginInitBinary it never uses HONEY_PLUGIN_INIT_PATH's file directly
// or the unsuffixed name — those identify a single (operator-arch) binary that
// may not match the remote host's arch.
func LocateShimBinaryForArch(goarch string) (string, error) {
	goarch = strings.TrimSpace(goarch)
	if goarch == "" {
		return "", fmt.Errorf("plugins: empty GOARCH for honey-plugin-init lookup")
	}
	name := "honey-plugin-init-linux-" + goarch
	var dirs []string
	if d := strings.TrimSpace(os.Getenv("HONEY_PLUGIN_INIT_DIR")); d != "" {
		dirs = append(dirs, d)
	}
	if p := strings.TrimSpace(os.Getenv("HONEY_PLUGIN_INIT_PATH")); p != "" {
		dirs = append(dirs, filepath.Dir(p))
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	tried := make([]string, 0, len(dirs))
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if validatePluginInitBinaryPath(p) == nil {
			return p, nil
		}
		tried = append(tried, p)
	}
	return "", fmt.Errorf("plugins: %s not found (looked in: %s; build both arches via `task build-honey-plugin-init`, then set HONEY_PLUGIN_INIT_DIR or HONEY_PLUGIN_INIT_PATH, or ship both alongside the honey binary)", name, strings.Join(tried, ", "))
}

// validatePluginInitBinaryPath rejects anything but a regular file — a
// directory at this path would otherwise be bind-mounted straight into the
// plugin container, where it fails only much later with a cryptic Docker/OCI
// runtime error ("is a directory: permission denied") instead of a clear one
// from honey itself.
func validatePluginInitBinaryPath(path string) error {
	info, err := safepath.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("path %q is not a regular file", path)
	}
	return nil
}

// Enabled reports whether plugins are turned on in config.
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	return m.enabled
}

// TimeoutMS returns configured plugin timeout milliseconds (default 30000).
func (m *Manager) TimeoutMS() int {
	if m == nil || m.timeoutMS <= 0 {
		return 30000
	}
	return m.timeoutMS
}

// List returns metadata for loaded plugins.
func (m *Manager) List() []Info {
	if m == nil {
		return nil
	}
	out := make([]Info, 0, len(m.plugins))
	for _, p := range m.plugins {
		out = append(out, Info{
			ID:                   p.manifest.ID,
			Version:              p.manifest.Version,
			Capabilities:         append([]string(nil), p.manifest.Capabilities...),
			Path:                 p.dir,
			SecretRefPrefixes:    append([]string(nil), p.manifest.SecretRefPrefixes...),
			AllowHostExec:        p.manifest.AllowHostExec,
			AllowRemoteExec:      p.manifest.AllowRemoteExec,
			AllowSFTP:            p.manifest.AllowSFTP,
			AllowTemplateRender:  p.manifest.AllowTemplateRender,
			AllowPostgres:        p.manifest.AllowPostgres,
			AllowKV:              p.manifest.AllowKV,
			AllowedHosts:         append([]string(nil), p.effectiveHosts...),
			AllowedPaths:         clonePathMap(p.effectivePaths),
			AllowedEnv:           append([]string(nil), p.manifest.AllowedEnv...),
			MaxHTTPResponseBytes: p.manifest.MaxHTTPResponseBytes,
		})
	}
	return out
}

// IsDockerPlugin reports whether pluginID is a loaded runtime:docker plugin
// (vs. a WASM plugin). The remote-host execution path (DockerHostSession)
// applies only to docker plugins.
func (m *Manager) IsDockerPlugin(pluginID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.byID[strings.TrimSpace(pluginID)]
	return ok && p != nil && p.manifest.effectiveRuntime() == "docker"
}

// EffectivePaths returns validated allowed_paths for a loaded plugin id.
func (m *Manager) EffectivePaths(pluginID string) map[string]string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.byID[strings.TrimSpace(pluginID)]
	if !ok || p == nil {
		return nil
	}
	return clonePathMap(p.effectivePaths)
}

// SecretRefPrefixes returns all registered secret ref prefixes from secret-capable plugins.
func (m *Manager) SecretRefPrefixes() []string {
	if m == nil {
		return nil
	}
	var out []string
	seen := make(map[string]struct{})
	for _, p := range m.plugins {
		if !p.manifest.hasCapability(CapSecret) {
			continue
		}
		for _, pref := range p.manifest.SecretRefPrefixes {
			pref = strings.TrimSpace(pref)
			if pref == "" {
				continue
			}
			if _, ok := seen[pref]; ok {
				continue
			}
			seen[pref] = struct{}{}
			out = append(out, pref)
		}
	}
	return out
}

// PluginIDsWithCapability returns plugin IDs that declare the capability.
func (m *Manager) PluginIDsWithCapability(capability string) []string {
	if m == nil {
		return nil
	}
	var ids []string
	for _, p := range m.plugins {
		if p.manifest.hasCapability(capability) {
			ids = append(ids, p.manifest.ID)
		}
	}
	return ids
}

// Call invokes export on pluginID with JSON input; decodes JSON output or returns plugin error string.
func (m *Manager) Call(ctx context.Context, pluginID, export string, in, out any) error {
	if m == nil || !m.enabled {
		return fmt.Errorf("plugins: disabled")
	}
	m.mu.Lock()
	lp, ok := m.byID[strings.TrimSpace(pluginID)]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("plugins: unknown plugin %q", pluginID)
	}
	inBytes, err := json.Marshal(in)
	if err != nil {
		return err
	}
	callCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		timeout := time.Duration(m.TimeoutMS()) * time.Millisecond
		callCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	lp.callMu.Lock()
	defer lp.callMu.Unlock()
	exit, outBytes, err := lp.transport.CallRaw(callCtx, export, inBytes)
	if err != nil {
		return fmt.Errorf("plugins: %s.%s: %w", pluginID, export, err)
	}
	var pe apiv1.PluginError
	if jsonErr := json.Unmarshal(outBytes, &pe); jsonErr == nil && strings.TrimSpace(pe.Error) != "" {
		return fmt.Errorf("plugins: %s.%s: %s", pluginID, export, pe.Error)
	}
	if exit != 0 {
		return fmt.Errorf("plugins: %s.%s: plugin returned exit code %d", pluginID, export, exit)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(outBytes, out); err != nil {
		return fmt.Errorf("plugins: %s.%s: decode output: %w", pluginID, export, err)
	}
	return nil
}

// TransformCue runs cue_transform plugins in manifest order.
func (m *Manager) TransformCue(ctx context.Context, cueBytes []byte, hostsCount int) ([]byte, error) {
	if m == nil || !m.enabled {
		return cueBytes, nil
	}
	out := cueBytes
	for _, p := range m.plugins {
		if !p.manifest.hasCapability(CapCueTransform) {
			continue
		}
		in := apiv1.CueTransformInput{
			APIVersion: apiv1.APIVersion,
			Cue:        encodeB64(out),
			HostsCount: hostsCount,
		}
		var resp apiv1.CueTransformOutput
		if err := m.Call(ctx, p.manifest.ID, "cue_transform", in, &resp); err != nil {
			return nil, fmt.Errorf("cue_transform plugin %q: %w", p.manifest.ID, err)
		}
		dec, err := decodeB64(resp.Cue)
		if err != nil {
			return nil, fmt.Errorf("cue_transform plugin %q: invalid cue output: %w", p.manifest.ID, err)
		}
		out = dec
	}
	return out, nil
}

// Close releases plugin resources.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var first error
	for _, p := range m.plugins {
		if p.transport != nil {
			if err := p.transport.Close(context.Background()); err != nil && first == nil {
				first = err
			}
		}
	}
	m.plugins = nil
	m.byID = make(map[string]*loadedPlugin)
	return first
}

// LoadFromDir is a test helper that loads plugins from a directory without config allowlist.
func LoadFromDir(ctx context.Context, dir string) (*Manager, error) {
	cfg := config.PluginsEffective{
		Enabled:     true,
		Directory:   dir,
		MaxMemoryMB: defaultPluginsMaxMemoryMB,
		TimeoutMS:   defaultPluginsTimeoutMS,
	}
	return NewManager(ctx, cfg)
}

const (
	defaultPluginsMaxMemoryMB = 32
	defaultPluginsTimeoutMS   = 30000
)

// PluginsFromConfig builds effective settings from honey config file.
//
//nolint:revive // plugins.PluginsFromConfig is the intended public name for this package.
func PluginsFromConfig(f *config.File) config.PluginsEffective {
	if f == nil {
		return config.Plugins{}.WithDefaults()
	}
	return f.Plugins.WithDefaults()
}
