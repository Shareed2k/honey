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
	rows := f.ListBackendRows()
	if len(rows) != 5 {
		t.Fatalf("ListBackendRows len=%d %+v", len(rows), rows)
	}
	if rows[0].Kind != "gcp" || rows[0].Name != "gcp-a" || rows[0].Hint != "p1" {
		t.Fatalf("row0: %+v", rows[0])
	}
	if rows[1].Kind != "gcp" || rows[1].Hint != "p2" {
		t.Fatalf("row1: %+v", rows[1])
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
}

func TestResolvePathXDG(t *testing.T) {
	dir := t.TempDir()
	hostdir := filepath.Join(dir, "hostctl")
	if err := os.MkdirAll(hostdir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(hostdir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOSTCTL_CONFIG", "")
	got, err := ResolvePath("")
	if err != nil || got != cfgPath {
		t.Fatalf("got %q err %v want %q", got, err, cfgPath)
	}
}
