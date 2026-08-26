package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadBackends(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
defaults:
  cache_ttl: 10m
  ssh_user: ops
backends:
  gcp:
    - name: gcp-a
      project: p1
      zone: z1
    - name: gcp-b
      project: p2
  aws:
    - name: aws-dev
      profile: dev
  kubernetes:
    - name: k8s-a
      context: ctx-a
      mode: pods
  consul:
    - name: consul-a
      addr: http://127.0.0.1:8500
      datacenter: dc1
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !f.HasAnyBackend() {
		t.Fatal("expected backends")
	}
	if len(f.Backends.GCP) != 2 || f.Backends.GCP[0].Name != "gcp-a" || f.Backends.GCP[0].Project != "p1" || f.Backends.GCP[0].Zone != "z1" {
		t.Fatalf("gcp: %+v", f.Backends.GCP)
	}
	if len(f.Backends.AWS) != 1 || f.Backends.AWS[0].Profile != "dev" {
		t.Fatalf("aws: %+v", f.Backends.AWS)
	}
	if len(f.Backends.Kubernetes) != 1 || f.Backends.Kubernetes[0].Context != "ctx-a" || f.Backends.Kubernetes[0].Mode != "pods" {
		t.Fatalf("kubernetes: %+v", f.Backends.Kubernetes)
	}
	if len(f.Backends.Consul) != 1 || f.Backends.Consul[0].Addr != "http://127.0.0.1:8500" {
		t.Fatalf("consul: %+v", f.Backends.Consul)
	}
	d, ok, err := f.Defaults.DefaultsCacheTTL()
	if err != nil || !ok || d.String() != "10m0s" {
		t.Fatalf("ttl: ok=%v d=%v err=%v", ok, d, err)
	}
	if f.Defaults.SSHUser != "ops" {
		t.Fatalf("ssh_user: %q", f.Defaults.SSHUser)
	}
}

func TestLoadAppsTTLStringDuration(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
apps:
  grafana:
    type: http
    upstream: http://127.0.0.1:3000
    local_port: 3001
    ttl: 5m
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Apps["grafana"].TTL; got != 5*time.Minute {
		t.Fatalf("ttl = %s, want 5m", got)
	}
}

func TestHasAnyBackendEmpty(t *testing.T) {
	t.Parallel()
	var f File
	if f.HasAnyBackend() {
		t.Fatal("expected false")
	}
	if (*File)(nil).HasAnyBackend() {
		t.Fatal("nil file")
	}
}

func TestParseYAMLInventoryVarsAcceptScalars(t *testing.T) {
	t.Parallel()
	const data = `
version: 1
inventory:
  vars:
    service: nginx
    allow_restart: true
    restart_timeout: 30
    ratio: 1.5
  groups:
    web:
      priority: 10
      match: "host.meta['role'] == 'web'"
      vars:
        health_url: http://127.0.0.1/health
  hosts:
    web-01:
      vars:
        service: nginx-canary
`
	f, err := ParseYAML([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Inventory.Vars["service"].String(); got != "nginx" {
		t.Fatalf("service = %q", got)
	}
	if got := f.Inventory.Vars["allow_restart"].Bool(); !got {
		t.Fatalf("allow_restart = %v", got)
	}
	if got := f.Inventory.Vars["restart_timeout"].String(); got != "30" {
		t.Fatalf("restart_timeout = %q", got)
	}
}

func TestParseYAMLInventoryVarsRejectNonScalars(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		yaml string
	}{
		{name: "map", yaml: "bad: {x: y}"},
		{name: "list", yaml: "bad: [a, b]"},
		{name: "null", yaml: "bad: null"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := "version: 1\ninventory:\n  vars:\n    " + tt.yaml + "\n"
			if _, err := ParseYAML([]byte(data)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestResolvePathExplicit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath(p)
	if err != nil || got != p {
		t.Fatalf("got %q err %v", got, err)
	}
	want := filepath.Join(dir, "records")
	if g := DefaultRecordDir(p); g != want {
		t.Fatalf("DefaultRecordDir(%q) = %q want %q", p, g, want)
	}
}

func TestResolveRecordDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	custom := filepath.Join(dir, "my-recs")

	t.Run("flag wins when changed", func(t *testing.T) {
		cfg := &File{Defaults: Defaults{RecordDir: "/from/config"}}
		got := ResolveRecordDir(cfg, cfgPath, "/from/flag", true)
		if got != "/from/flag" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("defaults.record_dir when flag not changed", func(t *testing.T) {
		cfg := &File{Defaults: Defaults{RecordDir: custom}}
		got := ResolveRecordDir(cfg, cfgPath, "", false)
		if got != custom {
			t.Fatalf("got %q want %q", got, custom)
		}
	})

	t.Run("DefaultRecordDir when no config field", func(t *testing.T) {
		cfg := &File{}
		want := filepath.Join(dir, "records")
		got := ResolveRecordDir(cfg, cfgPath, "", false)
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	})

	// With no config path to anchor to, the default is the OS cache directory —
	// never "", which used to silently disable recording. The exact root differs
	// per platform (XDG_CACHE_HOME or ~/.cache on Linux, ~/Library/Caches on
	// macOS), so assert the honey/records suffix, not the root. No t.Setenv here:
	// the parent test is parallel, where it is not allowed.
	t.Run("cache dir default when no config path", func(t *testing.T) {
		got := ResolveRecordDir(&File{}, "", "", false)
		if got == "" {
			t.Fatal("ResolveRecordDir returned empty: recording would be silently disabled")
		}
		if want := filepath.Join("honey", "records"); !strings.HasSuffix(got, want) {
			t.Fatalf("got %q, want a path ending in %q", got, want)
		}
	})
}

func TestResolvePathXDG(t *testing.T) {
	dir := t.TempDir()
	hostdir := filepath.Join(dir, "honey")
	if err := os.MkdirAll(hostdir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(hostdir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HONEY_CONFIG", "")
	got, err := ResolvePath("")
	if err != nil || got != cfgPath {
		t.Fatalf("got %q err %v want %q", got, err, cfgPath)
	}
}

func TestResolvePathExplicitWinsOverEnvAndFallback(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.yaml")
	if err := os.WriteFile(explicit, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, "env.yaml")
	if err := os.WriteFile(envPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HONEY_CONFIG", envPath)

	got, err := ResolvePath(explicit)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(explicit)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathHoneyConfigWinsOverFallback(t *testing.T) {
	dir := t.TempDir()
	honeyCfg := filepath.Join(dir, "honey-config.yaml")
	if err := os.WriteFile(honeyCfg, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	xdgBase := filepath.Join(dir, "xdg")
	xdgCfgDir := filepath.Join(xdgBase, "honey")
	if err := os.MkdirAll(xdgCfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fallbackCfg := filepath.Join(xdgCfgDir, "config.yaml")
	if err := os.WriteFile(fallbackCfg, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HONEY_CONFIG", honeyCfg)
	t.Setenv("XDG_CONFIG_HOME", xdgBase)

	got, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(honeyCfg)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathMissingHoneyConfigDoesNotFallback(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist.yaml")

	xdgBase := filepath.Join(dir, "xdg")
	xdgCfgDir := filepath.Join(xdgBase, "honey")
	if err := os.MkdirAll(xdgCfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fallbackCfg := filepath.Join(xdgCfgDir, "config.yaml")
	if err := os.WriteFile(fallbackCfg, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HONEY_CONFIG", missing)
	t.Setenv("XDG_CONFIG_HOME", xdgBase)

	got, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(missing)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestResolvePathFallbackPrefersXDGThenHomeFile(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("HONEY_CONFIG", "")

	homeCfgDir := filepath.Join(home, ".config", "honey")
	if err := os.MkdirAll(homeCfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	homeCfg := filepath.Join(homeCfgDir, "config.yaml")
	if err := os.WriteFile(homeCfg, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	homeLegacy := filepath.Join(home, ".honey.yaml")
	if err := os.WriteFile(homeLegacy, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	xdgBase := filepath.Join(dir, "xdg")
	xdgCfgDir := filepath.Join(xdgBase, "honey")
	if err := os.MkdirAll(xdgCfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	xdgCfg := filepath.Join(xdgCfgDir, "config.yaml")
	if err := os.WriteFile(xdgCfg, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdgBase)

	got, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if got != xdgCfg {
		t.Fatalf("got %q want %q", got, xdgCfg)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	got, err = ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if got != homeCfg {
		t.Fatalf("got %q want %q", got, homeCfg)
	}

	if err := os.Remove(homeCfg); err != nil {
		t.Fatal(err)
	}
	got, err = ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	if got != homeLegacy {
		t.Fatalf("got %q want %q", got, homeLegacy)
	}
}

func TestLoadUsesYAMLTags(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
alert_webhook:
  enabled: true
  port: 9099
  auto_investigate: true
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !f.AlertWebhook.Enabled {
		t.Fatal("alert_webhook.enabled should be true")
	}
	if f.AlertWebhook.Port != 9099 {
		t.Fatalf("alert_webhook.port = %d want %d", f.AlertWebhook.Port, 9099)
	}
	if !f.AlertWebhook.AutoInvestigate {
		t.Fatal("alert_webhook.auto_investigate should be true")
	}
}

func TestParseYAMLAppliesDefaultTags(t *testing.T) {
	t.Parallel()

	f, err := ParseYAML([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}

	if f.Defaults.Logs.AnomalyThresh != 0.9 {
		t.Fatalf("defaults.logs.anomaly_threshold = %v want 0.9", f.Defaults.Logs.AnomalyThresh)
	}
	if f.Defaults.Logs.AnomalyLLMModel != "llama3" {
		t.Fatalf("defaults.logs.anomaly_llm_model = %q want %q", f.Defaults.Logs.AnomalyLLMModel, "llama3")
	}
	if f.Defaults.Logs.AnomalyContextLines != 5 {
		t.Fatalf("defaults.logs.anomaly_context_lines = %d want %d", f.Defaults.Logs.AnomalyContextLines, 5)
	}
	if f.Defaults.Logs.AnomalyFreqWindow != 100 {
		t.Fatalf("defaults.logs.anomaly_freq_window = %d want %d", f.Defaults.Logs.AnomalyFreqWindow, 100)
	}
	if f.Defaults.Logs.AnomalyFreqRatio != 5.0 {
		t.Fatalf("defaults.logs.anomaly_freq_ratio = %v want 5.0", f.Defaults.Logs.AnomalyFreqRatio)
	}
}

func TestSaveValidation(t *testing.T) {
	t.Run("invalid config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")

		f := &File{
			Version: 1,
			Backends: Backends{
				Honey: []HoneyBackend{{Name: "", URL: "not-a-url"}}, // Invalid: empty name, bad URL format
			},
		}
		err := f.Save(path)
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}
		if !strings.Contains(err.Error(), "validation") {
			t.Fatalf("expected validation error, got: %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("file should not exist if validation failed")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")

		f := &File{
			Version: 1,
			Backends: Backends{
				Honey: []HoneyBackend{{Name: "test", URL: "https://honey.local"}},
			},
		}
		err := f.Save(path)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			t.Fatal("file should exist after successful save")
		}
	})
}

func TestParseSMTPConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
smtp:
  host: "smtp.example.com"
  port: 587
  username: "testuser"
  password: "testpassword"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.SMTP == nil {
		t.Fatal("expected SMTP config to be parsed")
	}
	if f.SMTP.Host != "smtp.example.com" {
		t.Errorf("expected host smtp.example.com, got %s", f.SMTP.Host)
	}
	if f.SMTP.Port != 587 {
		t.Errorf("expected port 587, got %d", f.SMTP.Port)
	}
	if f.SMTP.Username != "testuser" {
		t.Errorf("expected username testuser, got %s", f.SMTP.Username)
	}
	if f.SMTP.Password != "testpassword" {
		t.Errorf("expected password testpassword, got %s", f.SMTP.Password)
	}
}

func TestExecTimeoutDuration(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"empty", "", 0},
		{"seconds", "30s", 30 * time.Second},
		{"minutes", "5m", 5 * time.Minute},
		{"whitespace", "  2m  ", 2 * time.Minute},
		{"invalid", "nonsense", 0},
		{"negative", "-5s", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Defaults{ExecTimeout: tt.in}.ExecTimeoutDuration()
			if got != tt.want {
				t.Errorf("ExecTimeoutDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseMeshConfigEnabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
mesh:
  enabled: true
  private_key: "test-key-material"
  relay_addrs:
    - /ip4/1.2.3.4/tcp/4001/p2p/12D3KooTestRelayAddr
  listen_mesh: true
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Mesh.Enabled {
		t.Fatal("mesh.enabled should be true")
	}
	if f.Mesh.PrivateKey != "test-key-material" {
		t.Fatalf("mesh.private_key = %q, want test-key-material", f.Mesh.PrivateKey)
	}
	if len(f.Mesh.RelayAddrs) != 1 || f.Mesh.RelayAddrs[0] != "/ip4/1.2.3.4/tcp/4001/p2p/12D3KooTestRelayAddr" {
		t.Fatalf("mesh.relay_addrs = %v, want 1 addr", f.Mesh.RelayAddrs)
	}
	if !f.Mesh.ListenMesh {
		t.Fatal("mesh.listen_mesh should be true")
	}
}

func TestParseMeshConfigEnabledMissingRequiredFields(t *testing.T) {
	t.Parallel()
	data := `
version: 1
mesh:
  enabled: true
`
	_, err := ParseYAML([]byte(data))
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "mesh.private_key") {
		t.Fatalf("expected mesh.private_key error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "mesh.relay_addrs") {
		t.Fatalf("expected mesh.relay_addrs error, got: %v", err)
	}
}

func TestParseMeshConfigDisabled(t *testing.T) {
	t.Parallel()
	data := `
version: 1
mesh:
  enabled: false
`
	f, err := ParseYAML([]byte(data))
	if err != nil {
		t.Fatalf("expected no error for disabled mesh, got: %v", err)
	}
	if f.Mesh.Enabled {
		t.Fatal("mesh.enabled should be false")
	}
	if f.Mesh.PrivateKey != "" {
		t.Fatalf("mesh.private_key should be empty when disabled, got %q", f.Mesh.PrivateKey)
	}
}

func TestParseMeshConfigAbsent(t *testing.T) {
	t.Parallel()
	data := `
version: 1
`
	f, err := ParseYAML([]byte(data))
	if err != nil {
		t.Fatalf("expected no error for absent mesh, got: %v", err)
	}
	if f.Mesh.Enabled {
		t.Fatal("mesh.enabled should be false when absent")
	}
	if f.Mesh.PrivateKey != "" {
		t.Fatalf("mesh.private_key should be empty when absent, got %q", f.Mesh.PrivateKey)
	}
	if len(f.Mesh.RelayAddrs) != 0 {
		t.Fatalf("mesh.relay_addrs should be empty when absent, got %v", f.Mesh.RelayAddrs)
	}
}

func TestHoneyBackendMesh(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
backends:
  honey:
    - name: remote-honey
      url: https://honey.example.com
      mesh: true
      mesh_addr: /ip4/1.2.3.4/udp/4001/quic-v1/p2p/12D3KooTestPeerAddr
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Backends.Honey) != 1 {
		t.Fatalf("expected 1 honey backend, got %d", len(f.Backends.Honey))
	}
	if !f.Backends.Honey[0].Mesh {
		t.Fatal("honey backend mesh flag should be true")
	}
	if f.Backends.Honey[0].MeshAddr != "/ip4/1.2.3.4/udp/4001/quic-v1/p2p/12D3KooTestPeerAddr" {
		t.Fatalf("honey backend mesh_addr = %q, want the configured multiaddr", f.Backends.Honey[0].MeshAddr)
	}
}

func TestHoneyBackendMeshRequiresMeshAddr(t *testing.T) {
	t.Parallel()
	data := `
version: 1
backends:
  honey:
    - name: remote-honey
      url: https://honey.example.com
      mesh: true
`
	_, err := ParseYAML([]byte(data))
	if err == nil {
		t.Fatal("expected validation error for mesh: true with empty mesh_addr, got nil")
	}
	if !strings.Contains(err.Error(), "mesh_addr") {
		t.Fatalf("expected mesh_addr validation error, got: %v", err)
	}
}

func TestHoneyBackendMeshAddrValidatesCleanly(t *testing.T) {
	t.Parallel()
	data := `
version: 1
backends:
  honey:
    - name: remote-honey
      url: https://honey.example.com
      mesh: true
      mesh_addr: /ip4/1.2.3.4/udp/4001/quic-v1/p2p/12D3KooTestPeerAddr
`
	f, err := ParseYAML([]byte(data))
	if err != nil {
		t.Fatalf("expected no error for mesh: true with non-empty mesh_addr, got: %v", err)
	}
	if f.Backends.Honey[0].MeshAddr == "" {
		t.Fatal("expected mesh_addr to be populated")
	}
}

func TestHoneyBackendMeshFalseIgnoresMeshAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		data string
	}{
		{
			name: "mesh false with mesh_addr present",
			data: `
version: 1
backends:
  honey:
    - name: remote-honey
      url: https://honey.example.com
      mesh: false
      mesh_addr: /ip4/1.2.3.4/udp/4001/quic-v1/p2p/12D3KooTestPeerAddr
`,
		},
		{
			name: "mesh and mesh_addr both absent",
			data: `
version: 1
backends:
  honey:
    - name: remote-honey
      url: https://honey.example.com
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseYAML([]byte(tt.data)); err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}
}

func TestLoadJitConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
jit:
  enabled: false
  store_path: "  /var/lib/honey/jit_grants.jsonl  "
  default_duration: "  2h  "
  max_duration: "8h"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Jit == nil {
		t.Fatal("expected non-nil Jit block")
	}
	if f.Jit.Enabled == nil || *f.Jit.Enabled {
		t.Fatalf("Enabled = %v, want false", f.Jit.Enabled)
	}
	// mod:"trim" should have stripped surrounding whitespace via Sanitize.
	if f.Jit.StorePath != "/var/lib/honey/jit_grants.jsonl" {
		t.Fatalf("StorePath = %q", f.Jit.StorePath)
	}
	if f.Jit.DefaultDuration != "2h" {
		t.Fatalf("DefaultDuration = %q", f.Jit.DefaultDuration)
	}
	if f.Jit.MaxDuration != "8h" {
		t.Fatalf("MaxDuration = %q", f.Jit.MaxDuration)
	}
}

func TestJitConfigAbsentMeansNil(t *testing.T) {
	t.Parallel()
	f, err := ParseYAML([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Jit != nil {
		t.Fatalf("expected nil Jit block when absent, got %+v", f.Jit)
	}
}

func TestJitConfigRoundTripYAML(t *testing.T) {
	t.Parallel()
	enabled := true
	f := File{
		Version: 1,
		Jit: &JitConfig{
			Enabled:         &enabled,
			StorePath:       "/tmp/jit_grants.jsonl",
			DefaultDuration: "1h",
			MaxDuration:     "12h",
		},
	}
	b, err := yaml.Marshal(&f)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseYAML(b)
	if err != nil {
		t.Fatalf("re-parse: %v\nyaml:\n%s", err, b)
	}
	if out.Jit == nil {
		t.Fatal("expected non-nil Jit block after round-trip")
	}
	if out.Jit.Enabled == nil || !*out.Jit.Enabled {
		t.Fatalf("Enabled = %v, want true", out.Jit.Enabled)
	}
	if out.Jit.StorePath != f.Jit.StorePath || out.Jit.DefaultDuration != f.Jit.DefaultDuration || out.Jit.MaxDuration != f.Jit.MaxDuration {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", out.Jit, f.Jit)
	}
}

func TestLoadK8sProxyConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
k8s_proxy:
  listen: "  0.0.0.0:6443  "
  policy_dir: "  /etc/honey/policy  "
  clusters:
    - name: "  prod  "
      kubeconfig: "  /etc/honey/kubeconfig-prod  "
      context: "  prod-ctx  "
      impersonate:
        user_from: "cn"
        default_groups:
          - "developers"
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.K8sProxy == nil {
		t.Fatal("expected non-nil K8sProxy block")
	}
	// mod:"trim" should have stripped surrounding whitespace via Sanitize.
	if f.K8sProxy.Listen != "0.0.0.0:6443" {
		t.Fatalf("Listen = %q", f.K8sProxy.Listen)
	}
	if f.K8sProxy.PolicyDir != "/etc/honey/policy" {
		t.Fatalf("PolicyDir = %q", f.K8sProxy.PolicyDir)
	}
	if len(f.K8sProxy.Clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(f.K8sProxy.Clusters))
	}
	c := f.K8sProxy.Clusters[0]
	if c.Name != "prod" {
		t.Fatalf("Name = %q", c.Name)
	}
	if c.Kubeconfig != "/etc/honey/kubeconfig-prod" {
		t.Fatalf("Kubeconfig = %q", c.Kubeconfig)
	}
	if c.Context != "prod-ctx" {
		t.Fatalf("Context = %q", c.Context)
	}
	if c.Impersonate.UserFrom != "cn" {
		t.Fatalf("UserFrom = %q", c.Impersonate.UserFrom)
	}
	if len(c.Impersonate.DefaultGroups) != 1 || c.Impersonate.DefaultGroups[0] != "developers" {
		t.Fatalf("DefaultGroups = %+v", c.Impersonate.DefaultGroups)
	}
	if err := f.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestK8sProxyConfigAbsentMeansNil(t *testing.T) {
	t.Parallel()
	f, err := ParseYAML([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.K8sProxy != nil {
		t.Fatalf("expected nil K8sProxy block when absent, got %+v", f.K8sProxy)
	}
}

func TestK8sProxyConfigRoundTripYAML(t *testing.T) {
	t.Parallel()
	f := File{
		Version: 1,
		K8sProxy: &K8sProxyConfig{
			Listen:    "0.0.0.0:6443",
			PolicyDir: "/etc/honey/policy",
			Clusters: []K8sProxyCluster{
				{
					Name:       "prod",
					Kubeconfig: "/etc/honey/kubeconfig-prod",
					Context:    "prod-ctx",
					Impersonate: K8sImpersonation{
						UserFrom:      "cn",
						DefaultGroups: []string{"developers"},
					},
				},
			},
		},
	}
	b, err := yaml.Marshal(&f)
	if err != nil {
		t.Fatal(err)
	}
	out, err := ParseYAML(b)
	if err != nil {
		t.Fatalf("re-parse: %v\nyaml:\n%s", err, b)
	}
	if out.K8sProxy == nil {
		t.Fatal("expected non-nil K8sProxy block after round-trip")
	}
	if len(out.K8sProxy.Clusters) != 1 {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", out.K8sProxy.Clusters, f.K8sProxy.Clusters)
	}
	got, want := out.K8sProxy.Clusters[0], f.K8sProxy.Clusters[0]
	if got.Name != want.Name || got.Kubeconfig != want.Kubeconfig || got.Context != want.Context ||
		got.Impersonate.UserFrom != want.Impersonate.UserFrom ||
		len(got.Impersonate.DefaultGroups) != len(want.Impersonate.DefaultGroups) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
	for i, g := range want.Impersonate.DefaultGroups {
		if got.Impersonate.DefaultGroups[i] != g {
			t.Fatalf("DefaultGroups round-trip mismatch: got %+v, want %+v", got.Impersonate.DefaultGroups, want.Impersonate.DefaultGroups)
		}
	}
}

func TestK8sProxyConfigValidateRequiresClusterName(t *testing.T) {
	t.Parallel()
	f := File{
		Version: 1,
		K8sProxy: &K8sProxyConfig{
			Clusters: []K8sProxyCluster{{Kubeconfig: "/etc/honey/kubeconfig-prod"}},
		},
	}
	if err := f.Validate(); err == nil {
		t.Fatal("expected validation error for cluster with empty name")
	}
}

func TestK8sProxyConfigValidateRejectsUnknownUserFrom(t *testing.T) {
	t.Parallel()
	f := File{
		Version: 1,
		K8sProxy: &K8sProxyConfig{
			Clusters: []K8sProxyCluster{{
				Name:        "prod",
				Impersonate: K8sImpersonation{UserFrom: "email"},
			}},
		},
	}
	if err := f.Validate(); err == nil {
		t.Fatal("expected validation error for unknown impersonate.user_from")
	}
}

func TestLoadOIDCAndDeviceCertTTLAndClusterLabels(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := `
version: 1
device_cert_ttl: 8h
oidc:
  issuer: https://issuer.example/realm
  client_id: honey-kube
  scopes:
    - groups
    - profile
  username_claim: email
  groups_claim: groups
k8s_proxy:
  clusters:
    - name: prod
      labels:
        env: dev
        region: eu
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.OIDC == nil {
		t.Fatal("expected OIDC block to parse")
	}
	if f.OIDC.Issuer != "https://issuer.example/realm" {
		t.Fatalf("oidc.issuer = %q", f.OIDC.Issuer)
	}
	if f.OIDC.ClientID != "honey-kube" {
		t.Fatalf("oidc.client_id = %q", f.OIDC.ClientID)
	}
	if f.OIDC.UsernameClaim != "email" || f.OIDC.GroupsClaim != "groups" {
		t.Fatalf("oidc claims = %q/%q", f.OIDC.UsernameClaim, f.OIDC.GroupsClaim)
	}
	if len(f.OIDC.Scopes) != 2 || f.OIDC.Scopes[0] != "groups" || f.OIDC.Scopes[1] != "profile" {
		t.Fatalf("oidc.scopes = %v", f.OIDC.Scopes)
	}
	if got := f.DeviceCertTTLValue(); got != 8*time.Hour {
		t.Fatalf("device_cert_ttl = %s, want 8h", got)
	}
	if len(f.K8sProxy.Clusters) != 1 {
		t.Fatalf("clusters = %d, want 1", len(f.K8sProxy.Clusters))
	}
	labels := f.K8sProxy.Clusters[0].Labels
	if labels["env"] != "dev" || labels["region"] != "eu" {
		t.Fatalf("cluster labels = %v", labels)
	}
}

func TestLoadNoOIDCAndDefaultDeviceCertTTL(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := "version: 1\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.OIDC != nil {
		t.Fatalf("expected nil OIDC when absent, got %+v", f.OIDC)
	}
	if got := f.DeviceCertTTLValue(); got != DefaultDeviceCertTTL {
		t.Fatalf("device_cert_ttl default = %s, want %s", got, DefaultDeviceCertTTL)
	}
	if got := DefaultDeviceCertTTL; got != 12*time.Hour {
		t.Fatalf("DefaultDeviceCertTTL = %s, want 12h", got)
	}
}

func TestDeviceCertTTLValueResolver(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file *File
		want time.Duration
	}{
		{name: "nil file", file: nil, want: DefaultDeviceCertTTL},
		{name: "empty", file: &File{}, want: DefaultDeviceCertTTL},
		{name: "explicit", file: &File{DeviceCertTTL: "2h30m"}, want: 2*time.Hour + 30*time.Minute},
		{name: "unparseable falls back", file: &File{DeviceCertTTL: "nope"}, want: DefaultDeviceCertTTL},
		{name: "non-positive falls back", file: &File{DeviceCertTTL: "0s"}, want: DefaultDeviceCertTTL},
		{name: "negative falls back", file: &File{DeviceCertTTL: "-5m"}, want: DefaultDeviceCertTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.file.DeviceCertTTLValue(); got != tt.want {
				t.Fatalf("DeviceCertTTLValue() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestLoadInterceptBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(`intercept:
  enabled: true
  agent_image: ghcr.io/example/mogate:latest
  default_mode: [egress, incoming]
  policy_dir: /etc/honey/policies
`), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Intercept == nil {
		t.Fatal("Intercept nil")
	}
	if !f.Intercept.Enabled || f.Intercept.AgentImage != "ghcr.io/example/mogate:latest" {
		t.Errorf("intercept = %+v", f.Intercept)
	}
	if len(f.Intercept.DefaultMode) != 2 || f.Intercept.DefaultMode[0] != "egress" {
		t.Errorf("default_mode = %v", f.Intercept.DefaultMode)
	}
}

func TestLoadNoInterceptBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("device_cert_ttl: 12h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if f.Intercept != nil {
		t.Errorf("expected nil Intercept, got %+v", f.Intercept)
	}
}

func TestInterceptSessionTTLValue(t *testing.T) {
	require.Equal(t, time.Hour, (*InterceptConfig)(nil).SessionTTLValue())
	require.Equal(t, time.Hour, (&InterceptConfig{}).SessionTTLValue())
	require.Equal(t, 30*time.Minute, (&InterceptConfig{SessionTTL: "30m"}).SessionTTLValue())
	require.Equal(t, time.Hour, (&InterceptConfig{SessionTTL: "garbage"}).SessionTTLValue())
	require.Equal(t, time.Hour, (&InterceptConfig{SessionTTL: "-5m"}).SessionTTLValue())
}

func TestInterceptSessionStoreValue(t *testing.T) {
	require.Equal(t, "memory", (*InterceptConfig)(nil).SessionStoreValue())
	require.Equal(t, "memory", (&InterceptConfig{}).SessionStoreValue())
	require.Equal(t, "sqlite", (&InterceptConfig{SessionStore: "SQLite"}).SessionStoreValue())
	require.Equal(t, "postgres", (&InterceptConfig{SessionStore: "postgres"}).SessionStoreValue())
	require.Equal(t, "sqlite", (&InterceptConfig{SessionStore: "  sqlite  "}).SessionStoreValue())
}
