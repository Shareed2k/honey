//go:build !wasip1 && !wasm

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildArgvUpgradeInstall(t *testing.T) {
	cfg := helmConfig{
		Release:   "myapp",
		Chart:     "oci://ghcr.io/example/charts/myapp",
		Namespace: "production",
		Version:   "1.4.2",
		Wait:      true,
		Timeout:   "5m",
		Atomic:    true,
	}
	argv, err := buildArgv("upgrade_install", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"helm upgrade --install myapp",
		"--namespace production",
		"--version 1.4.2",
		"--wait",
		"--timeout 5m",
		"--atomic",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
}

func TestBuildArgvStatus(t *testing.T) {
	cfg := helmConfig{Release: "myapp", Namespace: "default"}
	argv, err := buildArgv("status", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "helm status myapp") {
		t.Errorf("expected 'helm status myapp', got %q", joined)
	}
	if !strings.Contains(joined, "--output json") {
		t.Errorf("expected '--output json', got %q", joined)
	}
}

func TestBuildArgvUnknownAction(t *testing.T) {
	_, err := buildArgv("frobnicate", helmConfig{Release: "x", Chart: "y"})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestBuildArgvMissingRelease(t *testing.T) {
	_, err := buildArgv("install", helmConfig{Chart: "mychart"})
	if err == nil {
		t.Fatal("expected error for missing release")
	}
}

func TestBuildArgvRepoAdd(t *testing.T) {
	cfg := helmConfig{Repo: "stable", RepoURL: "https://charts.helm.sh/stable"}
	argv, err := buildArgv("repo_add", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "helm repo add stable https://charts.helm.sh/stable") {
		t.Errorf("unexpected argv: %q", joined)
	}
}

func TestIsChangingAction(t *testing.T) {
	changing := []string{"install", "upgrade", "upgrade_install", "uninstall", "rollback"}
	readonly := []string{"status", "list", "template", "repo_add", "repo_update"}
	for _, a := range changing {
		if !isChangingAction(a) {
			t.Errorf("expected %q to be changing", a)
		}
	}
	for _, a := range readonly {
		if isChangingAction(a) {
			t.Errorf("expected %q to be non-changing", a)
		}
	}
}

func TestHelmConfigJSONRoundtrip(t *testing.T) {
	cfg := helmConfig{
		Release:   "myapp",
		Chart:     "oci://ghcr.io/example/myapp",
		Namespace: "production",
		Values:    map[string]any{"replicaCount": 3, "image": map[string]any{"tag": "v1.0"}},
		Set:       map[string]string{"global.debug": "true"},
		Wait:      true,
		Timeout:   "5m",
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got helmConfig
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Release != cfg.Release || got.Namespace != cfg.Namespace {
		t.Errorf("roundtrip mismatch: %+v vs %+v", got, cfg)
	}
}

func TestMergeSetJSON(t *testing.T) {
	values := map[string]any{
		"replicaCount": 3,
		"service":      map[string]any{"port": 8080},
	}
	flags := MergeSetJSONForTest(values)
	if len(flags) == 0 {
		t.Fatal("expected non-empty flags")
	}
	joined := strings.Join(flags, " ")
	if !strings.Contains(joined, "--set-json") {
		t.Errorf("expected --set-json in flags: %q", joined)
	}
}

func TestSetFlagsInArgv(t *testing.T) {
	cfg := helmConfig{
		Release: "app",
		Chart:   "mychart",
		Values:  map[string]any{"replicas": 2},
		Set:     map[string]string{"image.tag": "latest"},
	}
	argv, err := buildArgv("install", cfg)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--set-json replicas=") {
		t.Errorf("expected --set-json replicas=, got: %q", joined)
	}
	if !strings.Contains(joined, "--set image.tag=latest") {
		t.Errorf("expected --set image.tag=latest, got: %q", joined)
	}
}
