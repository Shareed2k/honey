// Package main implements the Honey helm WASM plugin.
package main

import (
	"encoding/json"
	"fmt"
)

func buildArgv(act string, cfg helmConfig) ([]string, error) {
	switch act {
	case "install":
		return buildInstallArgv(cfg)
	case "upgrade":
		return buildUpgradeArgv(cfg, false)
	case "upgrade_install":
		return buildUpgradeArgv(cfg, true)
	case "uninstall":
		return buildUninstallArgv(cfg)
	case "rollback":
		return buildRollbackArgv(cfg)
	case "status":
		return buildStatusArgv(cfg)
	case "template":
		return buildTemplateArgv(cfg)
	case "list":
		return buildListArgv(cfg)
	case "repo_add":
		return buildRepoAddArgv(cfg)
	case "repo_update":
		return buildRepoUpdateArgv(cfg)
	default:
		return nil, fmt.Errorf("unknown helm action %q", act)
	}
}

func buildInstallArgv(cfg helmConfig) ([]string, error) {
	if cfg.Release == "" {
		return nil, fmt.Errorf("release is required for install")
	}
	if cfg.Chart == "" {
		return nil, fmt.Errorf("chart is required for install")
	}
	gf, cf := globalFlags(cfg), chartFlags(cfg)
	args := make([]string, 0, 4+len(gf)+len(cf))
	args = append(args, "helm", "install", cfg.Release, cfg.Chart)
	args = append(args, gf...)
	args = append(args, cf...)
	return args, nil
}

func buildUpgradeArgv(cfg helmConfig, install bool) ([]string, error) {
	if cfg.Release == "" || cfg.Chart == "" {
		return nil, fmt.Errorf("release and chart are required for upgrade/upgrade_install")
	}
	args := []string{"helm", "upgrade"}
	if install {
		args = append(args, "--install")
	}
	args = append(args, cfg.Release, cfg.Chart)
	args = append(args, globalFlags(cfg)...)
	args = append(args, chartFlags(cfg)...)
	return args, nil
}

func buildUninstallArgv(cfg helmConfig) ([]string, error) {
	if cfg.Release == "" {
		return nil, fmt.Errorf("release is required for uninstall")
	}
	args := []string{"helm", "uninstall", cfg.Release}
	args = append(args, globalFlags(cfg)...)
	if cfg.Wait {
		args = append(args, "--wait")
	}
	if cfg.Timeout != "" {
		args = append(args, "--timeout", cfg.Timeout)
	}
	return args, nil
}

func buildRollbackArgv(cfg helmConfig) ([]string, error) {
	if cfg.Release == "" {
		return nil, fmt.Errorf("release is required for rollback")
	}
	args := []string{"helm", "rollback", cfg.Release}
	if cfg.Revision > 0 {
		args = append(args, fmt.Sprintf("%d", cfg.Revision))
	}
	args = append(args, globalFlags(cfg)...)
	if cfg.Wait {
		args = append(args, "--wait")
	}
	if cfg.Timeout != "" {
		args = append(args, "--timeout", cfg.Timeout)
	}
	return args, nil
}

func buildStatusArgv(cfg helmConfig) ([]string, error) {
	if cfg.Release == "" {
		return nil, fmt.Errorf("release is required for status")
	}
	gf := globalFlags(cfg)
	args := make([]string, 0, 5+len(gf))
	args = append(args, "helm", "status", cfg.Release, "--output", "json")
	args = append(args, gf...)
	return args, nil
}

func buildTemplateArgv(cfg helmConfig) ([]string, error) {
	if cfg.Release == "" || cfg.Chart == "" {
		return nil, fmt.Errorf("release and chart are required for template")
	}
	gf, cf := globalFlags(cfg), chartFlags(cfg)
	args := make([]string, 0, 4+len(gf)+len(cf))
	args = append(args, "helm", "template", cfg.Release, cfg.Chart)
	args = append(args, gf...)
	args = append(args, cf...)
	return args, nil
}

func buildListArgv(cfg helmConfig) ([]string, error) {
	args := []string{"helm", "list", "--output", "json"}
	args = append(args, globalFlags(cfg)...)
	if cfg.Namespace == "" {
		args = append(args, "--all-namespaces")
	}
	return args, nil
}

func buildRepoAddArgv(cfg helmConfig) ([]string, error) {
	if cfg.Repo == "" || cfg.RepoURL == "" {
		return nil, fmt.Errorf("repo and repo_url are required for repo_add")
	}
	return []string{"helm", "repo", "add", cfg.Repo, cfg.RepoURL}, nil
}

func buildRepoUpdateArgv(cfg helmConfig) ([]string, error) {
	args := []string{"helm", "repo", "update"}
	if cfg.Repo != "" {
		args = append(args, cfg.Repo)
	}
	return args, nil
}

func globalFlags(cfg helmConfig) []string {
	var flags []string
	if cfg.Namespace != "" {
		flags = append(flags, "--namespace", cfg.Namespace)
	}
	if cfg.Kubeconfig != "" {
		flags = append(flags, "--kubeconfig", cfg.Kubeconfig)
	}
	if cfg.Context != "" {
		flags = append(flags, "--kube-context", cfg.Context)
	}
	return flags
}

func chartFlags(cfg helmConfig) []string {
	var flags []string
	if cfg.Version != "" {
		flags = append(flags, "--version", cfg.Version)
	}
	if cfg.Wait {
		flags = append(flags, "--wait")
	}
	if cfg.Timeout != "" {
		flags = append(flags, "--timeout", cfg.Timeout)
	}
	if cfg.Atomic {
		flags = append(flags, "--atomic")
	}
	if cfg.Force {
		flags = append(flags, "--force")
	}
	for k, v := range cfg.Values {
		b, err := json.Marshal(v)
		if err == nil {
			flags = append(flags, "--set-json", k+"="+string(b))
		}
	}
	for k, v := range cfg.Set {
		flags = append(flags, "--set", k+"="+v)
	}
	return flags
}

func isChangingAction(act string) bool {
	switch act {
	case "install", "upgrade", "upgrade_install", "uninstall", "rollback":
		return true
	}
	return false
}

func valuesSetJSON(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	flags := make([]string, 0, len(values)*2)
	for k, v := range values {
		b, err := json.Marshal(v)
		if err != nil {
			continue
		}
		flags = append(flags, "--set-json", k+"="+string(b))
	}
	return flags
}

// MergeSetJSONForTest is exported for unit tests.
func MergeSetJSONForTest(values map[string]any) []string {
	return valuesSetJSON(values)
}

// BuildArgvForTest is exported for unit tests.
func BuildArgvForTest(act string, cfg helmConfig) ([]string, error) {
	return buildArgv(act, cfg)
}

// IsChangingActionForTest is exported for unit tests.
func IsChangingActionForTest(act string) bool {
	return isChangingAction(act)
}
