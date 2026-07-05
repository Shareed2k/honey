package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
}

func (m Manifest) hasCapability(name string) bool {
	for _, c := range m.Capabilities {
		if strings.TrimSpace(c) == name {
			return true
		}
	}
	return false
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
	return m, nil
}

func discoverPluginDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
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
		wasmPath := filepath.Join(dir, "plugin.wasm")
		if st, err := os.Stat(manifestPath); err == nil && !st.IsDir() {
			if st2, err2 := os.Stat(wasmPath); err2 == nil && !st2.IsDir() {
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs, nil
}
