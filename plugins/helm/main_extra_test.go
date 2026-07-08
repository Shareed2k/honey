//go:build !wasip1 && !wasm

package main

import (
	"strings"
	"testing"
)

func TestBuildArgvUninstall(t *testing.T) {
	cfg := helmConfig{
		Release: "myapp",
		Wait:    true,
		Timeout: "10m",
	}
	argv, err := buildArgv("uninstall", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"helm uninstall myapp",
		"--wait",
		"--timeout 10m",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
}

func TestBuildArgvRollback(t *testing.T) {
	cfg := helmConfig{
		Release:  "myapp",
		Revision: 2,
		Wait:     true,
		Timeout:  "5m",
	}
	argv, err := buildArgv("rollback", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"helm rollback myapp 2",
		"--wait",
		"--timeout 5m",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
}

func TestBuildArgvTemplate(t *testing.T) {
	cfg := helmConfig{
		Release:   "myapp",
		Chart:     "mychart",
		Namespace: "default",
	}
	argv, err := buildArgv("template", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"helm template myapp mychart",
		"--namespace default",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
}

func TestBuildArgvList(t *testing.T) {
	cfg := helmConfig{}
	argv, err := buildArgv("list", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"helm list --output json",
		"--all-namespaces",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}

	cfgNS := helmConfig{Namespace: "default"}
	argvNS, _ := buildArgv("list", cfgNS)
	joinedNS := strings.Join(argvNS, " ")
	if strings.Contains(joinedNS, "--all-namespaces") {
		t.Errorf("argv %q should not have --all-namespaces when namespace is set", joinedNS)
	}
	if !strings.Contains(joinedNS, "--namespace default") {
		t.Errorf("argv %q missing --namespace default", joinedNS)
	}
}

func TestBuildArgvRepoUpdate(t *testing.T) {
	cfg := helmConfig{}
	argv, err := buildArgv("repo_update", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	joined := strings.Join(argv, " ")
	if joined != "helm repo update" {
		t.Errorf("expected 'helm repo update', got %q", joined)
	}

	cfgRepo := helmConfig{Repo: "stable"}
	argvRepo, _ := buildArgv("repo_update", cfgRepo)
	joinedRepo := strings.Join(argvRepo, " ")
	if joinedRepo != "helm repo update stable" {
		t.Errorf("expected 'helm repo update stable', got %q", joinedRepo)
	}
}

func TestBuildArgvMissingFields(t *testing.T) {
	_, err := buildArgv("uninstall", helmConfig{})
	if err == nil {
		t.Error("expected error for uninstall with no release")
	}
	_, err = buildArgv("rollback", helmConfig{})
	if err == nil {
		t.Error("expected error for rollback with no release")
	}
	_, err = buildArgv("template", helmConfig{Release: "r"})
	if err == nil {
		t.Error("expected error for template with no chart")
	}
	_, err = buildArgv("repo_add", helmConfig{Repo: "r"})
	if err == nil {
		t.Error("expected error for repo_add with no repo URL")
	}
}
