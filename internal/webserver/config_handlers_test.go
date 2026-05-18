package webserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
)

func TestConfigPutReloadsInMemoryDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Options{
		ListenAddr: "127.0.0.1:0",
		Token:      "secret",
		Version:    "0",
		ConfigPath: cfgPath,
		Config:     &config.File{Defaults: config.Defaults{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.sshUser(""); got != "" {
		t.Fatalf("before PUT sshUser = %q, want empty", got)
	}

	body := []byte("version: 1\ndefaults:\n  ssh_user: ops\n")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/yaml")
	s.withAuth(s.handleConfigPut)(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT %d: %s", rec.Code, rec.Body.String())
	}
	if got := s.sshUser(""); got != "ops" {
		t.Fatalf("after PUT sshUser = %q, want ops", got)
	}
	if s.opts.Config == nil {
		t.Fatal("opts.Config is nil after PUT")
	}
	if s.opts.Config.Defaults.SSHUser != "ops" {
		t.Fatalf("opts.Config.Defaults.SSHUser = %q, want ops", s.opts.Config.Defaults.SSHUser)
	}
	if !strings.HasSuffix(strings.TrimSpace(s.opts.ConfigPath), "config.yaml") {
		t.Fatalf("opts.ConfigPath = %q", s.opts.ConfigPath)
	}
}
