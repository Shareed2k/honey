//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/provider/localprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"
)

func TestHoneyProviderE2E_Files(t *testing.T) {
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
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	targetRec := recs[0]

	ep, ok := factory.(searchrun.ExecutorProvider)
	if !ok {
		t.Fatalf("factory does not implement ExecutorProvider")
	}

	executor := ep.ExecutorFor(targetRec, nil)
	if executor == nil {
		t.Fatalf("expected executor, got nil")
	}

	client, err := executor.Dial("testuser", targetRec)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}

	// 1. Mkdir
	if err := client.MkdirAllRemote("/tmp/proxy_test_dir"); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// 2. Stat directory
	stat, err := client.StatRemote("/tmp/proxy_test_dir")
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if !stat.IsDir || stat.Name != "proxy_test_dir" {
		t.Fatalf("unexpected stat result: %+v", stat)
	}

	// 3. Create a file directly with Run to test List
	if _, err := client.Run("touch /tmp/proxy_test_dir/file.txt"); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	// 4. List
	entries, err := client.ListRemoteDir("/tmp/proxy_test_dir")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "file.txt" {
		t.Fatalf("unexpected list result: %+v", entries)
	}

	// 5. Remove
	if err := client.RemoveRemote("/tmp/proxy_test_dir", true); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	// 6. Verify removed
	if _, err := client.StatRemote("/tmp/proxy_test_dir"); err == nil {
		t.Fatalf("expected stat to fail after remove")
	}
}
