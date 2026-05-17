package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	AllowKV              bool              `json:"allow_kv,omitempty"`
	AllowedHosts         []string          `json:"allowed_hosts,omitempty"`
	AllowedPaths         map[string]string `json:"allowed_paths,omitempty"`
	AllowedEnv           []string          `json:"allowed_env,omitempty"`
	MaxHTTPResponseBytes int64             `json:"max_http_response_bytes,omitempty"`
}

// Manager loads Extism WASM plugins and routes capability calls.
type Manager struct {
	mu      sync.Mutex
	enabled bool
	plugins []*loadedPlugin
	byID    map[string]*loadedPlugin
}

type loadedPlugin struct {
	manifest       Manifest
	effectiveHosts []string
	effectivePaths map[string]string
	dir            string
	wasm           []byte
	plugin         *extism.Plugin
	callMu         sync.Mutex // extism.Plugin is not safe for concurrent CallWithContext
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
		enabled: cfg.Enabled,
		byID:    make(map[string]*loadedPlugin),
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
	wasmPath := filepath.Join(dir, "plugin.wasm")
	manifest, err := loadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	hosts, paths, err := validateManifestPolicy(manifest, cfg)
	if err != nil {
		return nil, err
	}
	wasm, err := safepath.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("plugins: read %s: %w", wasmPath, err)
	}
	manifestCfg := map[string]string{
		"plugin_id": manifest.ID,
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
		plugin:         plug,
	}, nil
}

// Enabled reports whether plugins are turned on in config.
func (m *Manager) Enabled() bool {
	if m == nil {
		return false
	}
	return m.enabled
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
			AllowKV:              p.manifest.AllowKV,
			AllowedHosts:         append([]string(nil), p.effectiveHosts...),
			AllowedPaths:         clonePathMap(p.effectivePaths),
			AllowedEnv:           append([]string(nil), p.manifest.AllowedEnv...),
			MaxHTTPResponseBytes: p.manifest.MaxHTTPResponseBytes,
		})
	}
	return out
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
		callCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	lp.callMu.Lock()
	defer lp.callMu.Unlock()
	exit, outBytes, err := lp.plugin.CallWithContext(callCtx, export, inBytes)
	if err != nil {
		return fmt.Errorf("plugins: %s.%s: %w", pluginID, export, err)
	}
	if exit != 0 {
		return fmt.Errorf("plugins: %s.%s: plugin returned exit code %d", pluginID, export, exit)
	}
	var pe apiv1.PluginError
	if err := json.Unmarshal(outBytes, &pe); err == nil && strings.TrimSpace(pe.Error) != "" {
		return fmt.Errorf("plugins: %s.%s: %s", pluginID, export, pe.Error)
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
		if p.plugin != nil {
			if err := p.plugin.Close(context.Background()); err != nil && first == nil {
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
