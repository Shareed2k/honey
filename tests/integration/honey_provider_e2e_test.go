//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"
	"github.com/shareed2k/honey/internal/provider/localprovider"
)

func TestHoneyProviderE2E(t *testing.T) {
	// 1. Create a real config file for the remote server with a "local" backend
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := `
version: 1
backends:
  local:
    - name: "prod"
      hosts:
        - name: "remote-host"
          primary_ip: "10.0.0.1"
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Create a real search registry containing the local provider
	remoteSearchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		localprovider.NewFactory(testLocalConfig{}),
	})

	// Boot up the real honey webserver
	opts := webserver.Options{
		SearchRegistry: remoteSearchReg,
		Token:          "test-token",
		ConfigPath:     configPath,
	}
	baseURL := newTestServer(t, opts)

	// Initialize the honey provider client pointing to the real webserver
	cfg := &config.File{
		Backends: config.Backends{
			Honey: []config.HoneyBackend{
				{Name: "remote1", URL: baseURL, Token: "test-token", Insecure: false},
			},
		},
	}

	classCfg := honeyTestConfig{cfg}
	factory := honeyprovider.NewFactory(classCfg)

	// Test 1: Fetch BackendRows (should hit /api/v1/config/backends)
	rows := factory.BackendRows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 backend rows (1 honey, 1 remote), got %d: %+v", len(rows), rows)
	}
	if rows[0].Kind != "honey" || rows[0].Name != "remote1" {
		t.Errorf("expected honey row first")
	}
	if rows[1].Kind != "local" || rows[1].Name != "prod" {
		t.Errorf("expected remote backend row second")
	}

	// Test 2: Search Hosts (should hit /api/v1/search)
	provider := factory.FromConfig(nil)[0]
	recs, err := provider.Search(context.Background(), hosts.Query{NameSubstring: "remote"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Name != "remote-host" {
		t.Errorf("unexpected record: %+v", recs[0])
	}
}

type honeyTestConfig struct {
	cfg *config.File
}

func (h honeyTestConfig) HoneyBackends() []config.HoneyBackend {
	return h.cfg.Backends.Honey
}

func (h honeyTestConfig) HoneyBackendSlicePtr() *[]config.HoneyBackend {
	return &h.cfg.Backends.Honey
}

func (h honeyTestConfig) SetHoneyBackends(b []config.HoneyBackend) {
	h.cfg.Backends.Honey = b
}

type testLocalConfig struct{}

func (testLocalConfig) LocalBackends() []config.LocalBackend {
	return []config.LocalBackend{
		{
			Name: "prod",
			Hosts: []config.LocalHost{
				{Name: "remote-host", PrimaryIP: "10.0.0.1"},
			},
		},
	}
}
func (testLocalConfig) LocalBackendSlicePtr() *[]config.LocalBackend { return nil }
func (testLocalConfig) SetLocalBackends([]config.LocalBackend)       {}
func (testLocalConfig) DockerDiscover() config.DockerDiscover        { return config.DockerDiscover{} }

type twoBackendLocalConfig struct{}

func (twoBackendLocalConfig) LocalBackends() []config.LocalBackend {
	return []config.LocalBackend{
		{
			Name: "backend-a",
			Hosts: []config.LocalHost{
				{Name: "host-a", PrimaryIP: "10.0.0.10"},
			},
		},
		{
			Name: "backend-b",
			Hosts: []config.LocalHost{
				{Name: "host-b", PrimaryIP: "10.0.0.20"},
			},
		},
	}
}
func (twoBackendLocalConfig) LocalBackendSlicePtr() *[]config.LocalBackend { return nil }
func (twoBackendLocalConfig) SetLocalBackends([]config.LocalBackend)       {}
func (twoBackendLocalConfig) DockerDiscover() config.DockerDiscover        { return config.DockerDiscover{} }

func TestHoneyProviderE2E_BackendFilter(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := `
version: 1
backends:
  local:
    - name: "backend-a"
      hosts:
        - name: "host-a"
          primary_ip: "10.0.0.10"
    - name: "backend-b"
      hosts:
        - name: "host-b"
          primary_ip: "10.0.0.20"
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	remoteSearchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		localprovider.NewFactory(twoBackendLocalConfig{}),
	})

	baseURL := newTestServer(t, webserver.Options{
		SearchRegistry: remoteSearchReg,
		Token:          "test-token",
		ConfigPath:     configPath,
	})

	cfg := &config.File{
		Backends: config.Backends{
			Honey: []config.HoneyBackend{
				{Name: "local-proxy", URL: baseURL, Token: "test-token"},
			},
		},
	}

	provider := honeyprovider.NewFactory(honeyTestConfig{cfg}).FromConfig(nil)[0]
	ctx := context.Background()

	hostNames := func(recs []hosts.Record) []string {
		names := make([]string, len(recs))
		for i, r := range recs {
			names[i] = r.Name
		}
		return names
	}

	t.Run("no filter returns both hosts", func(t *testing.T) {
		recs, err := provider.Search(ctx, hosts.Query{})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		names := hostNames(recs)
		if !contains(names, "host-a") || !contains(names, "host-b") {
			t.Errorf("expected both hosts, got %v", names)
		}
	})

	t.Run("sub-backend filter forwards to server", func(t *testing.T) {
		recs, err := provider.Search(ctx, hosts.Query{Backends: []string{"backend-a"}})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		names := hostNames(recs)
		if !contains(names, "host-a") {
			t.Errorf("expected host-a, got %v", names)
		}
		if contains(names, "host-b") {
			t.Errorf("expected no host-b, got %v", names)
		}
	})

	t.Run("proxy own name stripped: server gets all", func(t *testing.T) {
		recs, err := provider.Search(ctx, hosts.Query{Backends: []string{"local-proxy"}})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		names := hostNames(recs)
		if !contains(names, "host-a") || !contains(names, "host-b") {
			t.Errorf("expected both hosts, got %v", names)
		}
	})

	t.Run("mixed filter: proxy name stripped sub-backend forwarded", func(t *testing.T) {
		recs, err := provider.Search(ctx, hosts.Query{Backends: []string{"local-proxy", "backend-b"}})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		names := hostNames(recs)
		if contains(names, "host-a") {
			t.Errorf("expected no host-a, got %v", names)
		}
		if !contains(names, "host-b") {
			t.Errorf("expected host-b, got %v", names)
		}
	})
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func TestHoneyProviderE2E_Exec(t *testing.T) {
	// We need the local ssh test server
	sshH, sshP, keyFile := startSSH(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := `
version: 1
backends:
  local:
    - name: "prod"
      hosts:
        - name: "remote-host"
          primary_ip: "127.0.0.1"
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	remoteSearchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		localprovider.NewFactory(testLocalConfig{}),
	})

	opts := webserver.Options{
		SearchRegistry: remoteSearchReg,
		ExecRegistry:   reg,
		Token:          "test-token",
		ConfigPath:     configPath,
	}
	baseURL := newTestServer(t, opts)

	cfg := &config.File{
		Backends: config.Backends{
			Honey: []config.HoneyBackend{
				{Name: "remote1", URL: baseURL, Token: "test-token", Insecure: false},
			},
		},
	}

	classCfg := honeyTestConfig{cfg}
	factory := honeyprovider.NewFactory(classCfg)
	provider := factory.FromConfig(nil)[0]

	// Find the record via Search
	recs, err := provider.Search(context.Background(), hosts.Query{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	targetRec := recs[0]

	// Validate the tag exists
	if targetRec.Meta["honey_upstream_backend"] != "remote1" {
		t.Fatalf("expected honey_upstream_backend tag on record, got %q", targetRec.Meta["honey_upstream_backend"])
	}

	// Now try to execute using the custom executor.
	// `factory` implements `ExecutorProvider`, let's check it.
	ep, ok := factory.(searchrun.ExecutorProvider)
	if !ok {
		t.Fatalf("factory does not implement ExecutorProvider")
	}

	if !ep.HandlesRecord(targetRec) {
		t.Fatalf("factory should handle the record")
	}

	executor := ep.ExecutorFor(targetRec, nil)
	if executor == nil {
		t.Fatalf("expected executor, got nil")
	}

	client, err := executor.Dial("testuser", targetRec)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	out, err := client.Run("echo hello proxy")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}

	if string(out) != "hello proxy" && string(out) != "hello proxy\n" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestHoneyProviderE2E_StreamsAndTunnels(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configYAML := `
version: 1
backends:
  local:
    - name: "prod"
      hosts:
        - name: "remote-host"
          primary_ip: "127.0.0.1"
`
	if err := os.WriteFile(configPath, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	remoteSearchReg := searchrun.NewRegistry([]searchrun.ProviderFactory{
		localprovider.NewFactory(testLocalConfig{}),
	})

	opts := webserver.Options{
		SearchRegistry: remoteSearchReg,
		ExecRegistry:   reg,
		Token:          "test-token",
		ConfigPath:     configPath,
	}
	baseURL := newTestServer(t, opts)

	cfg := &config.File{
		Backends: config.Backends{
			Honey: []config.HoneyBackend{
				{Name: "remote1", URL: baseURL, Token: "test-token", Insecure: false},
			},
		},
	}

	classCfg := honeyTestConfig{cfg}
	factory := honeyprovider.NewFactory(classCfg)
	provider := factory.FromConfig(nil)[0]

	recs, err := provider.Search(context.Background(), hosts.Query{})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	targetRec := recs[0]

	ep, _ := factory.(searchrun.ExecutorProvider)
	executor := ep.ExecutorFor(targetRec, nil)
	client, err := executor.Dial("testuser", targetRec)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	
	// Test RunWithStreams
	var outBuf bytes.Buffer
	err = client.RunWithStreams("echo stream proxy", nil, &outBuf, nil)
	if err != nil {
		t.Fatalf("RunWithStreams failed: %v", err)
	}
	if outBuf.String() != "stream proxy\n" && outBuf.String() != "stream proxy" {
		t.Fatalf("unexpected stream output: %q", outBuf.String())
	}
	
	// Test Tunnel Upstream
	// Since SSH starts with a banner, if we read it, we know the tunnel works.
	conn, err := executor.DialUpstream(context.Background(), "testuser", targetRec, fmt.Sprintf("127.0.0.1:%d", sshP))
	if err != nil {
		t.Fatalf("DialUpstream failed: %v", err)
	}
	defer conn.Close()
	
	banner := make([]byte, 20)
	n, err := conn.Read(banner)
	if err != nil {
		t.Fatalf("tunnel read failed: %v", err)
	}
	if !strings.Contains(string(banner[:n]), "SSH-") {
		t.Fatalf("expected SSH banner, got %q", string(banner[:n]))
	}
}
