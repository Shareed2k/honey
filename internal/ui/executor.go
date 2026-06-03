package ui

import (
	"fmt"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
)

// HostClient aliases the shared execution interface (see internal/hostexec).
type HostClient = hostexec.HostClient

// Executor aliases the shared executor interface.
type Executor = hostexec.Executor

// RemoteFileEntry aliases remote file metadata for JSON APIs.
type RemoteFileEntry = hostexec.RemoteFileEntry

// FormatTargetForDryRun returns a string describing how the target will be connected to.
func FormatTargetForDryRun(r hosts.Record) string {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return fmt.Sprintf("k8s_exec(ns=%s pod=%s)", r.Meta["namespace"], r.Meta["pod_name"])
	}
	if r.Provider == "proxmox" {
		if b, ok := proxmoxprovider.BackendByName(r.Meta["backend_name"]); ok {
			switch b.ExecMode {
			case proxmoxprovider.ProxmoxExecPVE:
				return fmt.Sprintf("proxmox_pve(node=%s vmid=%s kind=%s)", r.Meta["node"], r.Meta["vmid"], r.Meta["kind"])
			case proxmoxprovider.ProxmoxExecHybrid:
				return fmt.Sprintf("proxmox_hybrid(node=%s vmid=%s kind=%s ip=%s)", r.Meta["node"], r.Meta["vmid"], r.Meta["kind"], r.PrimaryIP)
			}
		}
	}
	return fmt.Sprintf("ip=%s", r.PrimaryIP)
}

// Type aliases so existing ui code can continue using the unexported names
// while the actual implementations live in the provider packages.
type (
	k8sPodExecutor     = k8sprovider.K8sPodExecutor
	k8sNativeClient    = k8sprovider.K8sNativeClient
	dockerNativeClient = dockerprovider.DockerNativeClient
)
