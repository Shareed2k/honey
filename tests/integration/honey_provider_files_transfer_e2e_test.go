//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/honeyprovider"
	"github.com/shareed2k/honey/internal/provider/localprovider"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/webserver"
)

func TestHoneyProviderE2E_FilesTransfer(t *testing.T) {
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
	if err := client.MkdirAllRemote("/tmp/proxy_test_dir_transfer"); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	defer client.RemoveRemote("/tmp/proxy_test_dir_transfer", true)

	// 2. Upload
	localUploadFile := filepath.Join(tmpDir, "upload.txt")
	err = os.WriteFile(localUploadFile, []byte("hello from upload proxy"), 0644)
	if err != nil {
		t.Fatalf("failed to create upload file: %v", err)
	}

	if err := client.Upload(localUploadFile, "/tmp/proxy_test_dir_transfer/uploaded.txt"); err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	
	// Wait for SSH file sync
	time.Sleep(2 * time.Second)

	// 3. Stat uploaded file to verify
	stat, err := client.StatRemote("/tmp/proxy_test_dir_transfer/uploaded.txt")
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if stat.IsDir {
		t.Fatalf("unexpected stat result: %+v", stat)
	}

	// 4. Download
	localDownloadFile := filepath.Join(tmpDir, "download.txt")
	if err := client.Download("/tmp/proxy_test_dir_transfer/uploaded.txt", localDownloadFile); err != nil {
		t.Fatalf("download failed: %v", err)
	}

	// 5. Verify downloaded content
	b, err := os.ReadFile(localDownloadFile)
	if err != nil {
		t.Fatalf("read downloaded file failed: %v", err)
	}
	if string(b) != "hello from upload proxy" {
		t.Fatalf("unexpected downloaded content: %q", string(b))
	}
}
