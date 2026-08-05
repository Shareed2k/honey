//go:build docker_e2e

// Package plugins docker_e2e test: exercises the keep_warm container reuse path
// against a REAL docker daemon. Excluded from normal `go test` (and CI) by the
// docker_e2e build tag — the rest of this package follows a no-real-daemon unit
// test convention. Run it explicitly against a reachable daemon:
//
//	GOOS/arch: build the shim for the DAEMON's platform first, e.g.
//	  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/honey-plugin-init ./cmd/honey-plugin-init
//	then:
//	  DOCKER_HOST=... HONEY_E2E_SHIM=/tmp/honey-plugin-init \
//	    go test -tags docker_e2e -run TestE2E_WarmPool -v ./internal/plugins/
//
// HONEY_E2E_IMAGE overrides the base image (default alpine:3.20). The test
// reaps every honey warm container it created before returning.
package plugins

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

func e2eImage() string {
	if v := os.Getenv("HONEY_E2E_IMAGE"); v != "" {
		return v
	}
	return "alpine:3.20"
}

func e2eBackendAndClient(t *testing.T) (*localBackend, *http.Client) {
	t.Helper()
	shim := os.Getenv("HONEY_E2E_SHIM")
	if shim == "" {
		t.Skip("HONEY_E2E_SHIM not set (path to a daemon-platform honey-plugin-init binary)")
	}
	if _, err := os.Stat(shim); err != nil {
		t.Fatalf("HONEY_E2E_SHIM %q: %v", shim, err)
	}
	backend, err := newLocalBackend(shim, os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatalf("newLocalBackend: %v", err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	httpClient := &http.Client{Transport: &http.Transport{DialContext: backend.DialShim}}
	return backend, httpClient
}

func countWarm(t *testing.T, cli WarmReaperClient, digest string) int {
	t.Helper()
	f := client.Filters{}.Add("label", warmLabelManaged+"=true")
	if digest != "" {
		f = f.Add("label", warmLabelDigest+"="+digest)
	}
	res, err := cli.ContainerList(context.Background(), client.ContainerListOptions{All: true, Filters: f})
	if err != nil {
		t.Fatalf("list warm: %v", err)
	}
	return len(res.Items)
}

func TestE2E_WarmPool(t *testing.T) {
	backend, httpClient := e2eBackendAndClient(t)
	cli := backend.Client()
	ctx := context.Background()

	cfg := dockerTransportConfig{
		Image:      e2eImage(),
		PullPolicy: "if_not_present",
		InitMode:   "bind",
		KeepWarm:   true,
		PluginID:   "e2e-warm",
		Env:        map[string]string{"HONEY_E2E": "1"},
	}
	digest := warmDigest(cfg)

	// Best-effort clean slate + guaranteed cleanup.
	_, _ = ReapWarmContainers(ctx, cli, 0, time.Now())
	t.Cleanup(func() { _, _ = ReapWarmContainers(ctx, cli, 0, time.Now()) })

	shim, _ := backend.ShimHostPath(ctx)

	// Run 1: cold start creates the named+labeled container.
	id1, addr1, err := createAndStart(ctx, cli, httpClient, shim, 0, cfg)
	if err != nil {
		t.Fatalf("run1 createAndStart: %v", err)
	}
	if id1 == "" || addr1 == "" {
		t.Fatalf("run1 empty id/addr: %q %q", id1, addr1)
	}
	if n := countWarm(t, cli, digest); n != 1 {
		t.Fatalf("after run1: %d warm containers, want 1", n)
	}

	// Run 2: a fresh "CLI run" must REUSE the same container — no create/start.
	id2, addr2, err := createAndStart(ctx, cli, httpClient, shim, 0, cfg)
	if err != nil {
		t.Fatalf("run2 createAndStart: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("run2 did not reuse: id2=%s id1=%s (new container created)", id2, id1)
	}
	if addr2 != addr1 {
		t.Fatalf("run2 addr changed: %s vs %s", addr2, addr1)
	}
	if n := countWarm(t, cli, digest); n != 1 {
		t.Fatalf("after run2: %d warm containers, want still 1 (reuse, not create)", n)
	}
	t.Logf("REUSE OK: run2 attached to run1 container %s at %s", id1[:12], addr1)

	// Different config (env change) → different digest → a NEW container.
	cfg2 := cfg
	cfg2.Env = map[string]string{"HONEY_E2E": "2"}
	digest2 := warmDigest(cfg2)
	if digest2 == digest {
		t.Fatal("expected different digest for changed env")
	}
	id3, _, err := createAndStart(ctx, cli, httpClient, shim, 0, cfg2)
	if err != nil {
		t.Fatalf("run3 createAndStart: %v", err)
	}
	if id3 == id1 {
		t.Fatalf("changed config reused old container %s", id1)
	}
	if n := countWarm(t, cli, ""); n != 2 {
		t.Fatalf("after run3: %d total warm containers, want 2", n)
	}
	t.Logf("REPLACE OK: changed config created new container %s (old %s kept)", id3[:12], id1[:12])

	// gc with a long TTL removes nothing (both are young).
	if removed, err := ReapWarmContainers(ctx, cli, time.Hour, time.Now()); err != nil || removed != 0 {
		t.Fatalf("gc --older-than 1h: removed=%d err=%v, want 0", removed, err)
	}
	// gc all removes both.
	removed, err := ReapWarmContainers(ctx, cli, 0, time.Now())
	if err != nil || removed != 2 {
		t.Fatalf("gc all: removed=%d err=%v, want 2", removed, err)
	}
	if n := countWarm(t, cli, ""); n != 0 {
		t.Fatalf("after gc: %d warm containers, want 0", n)
	}
	t.Logf("GC OK: reaped both warm containers")
}
