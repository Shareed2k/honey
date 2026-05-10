package config

import (
	"os"
	"path/filepath"
	"testing"
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
    - project: p2
  aws:
    - profile: dev
  kubernetes:
    - context: ctx-a
      mode: pods
  consul:
    - addr: 127.0.0.1:8500
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
	if len(f.Backends.Consul) != 1 || f.Backends.Consul[0].Addr != "127.0.0.1:8500" {
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
