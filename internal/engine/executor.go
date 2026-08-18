package engine

import (
	"fmt"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
)

// HostClient aliases the shared execution interface (see internal/hostexec).
// HostClient ...
type HostClient = hostexec.HostClient

// Executor aliases the shared executor interface.
// Executor ...
type Executor = hostexec.Executor

// RemoteFileEntry aliases remote file metadata for JSON APIs.
// RemoteFileEntry ...
type RemoteFileEntry = hostexec.RemoteFileEntry

// FormatTargetForDryRun returns a string describing how the target will be connected to.
// FormatTargetForDryRun ...
func FormatTargetForDryRun(r hosts.Record) string {
	if r.IsPod() {
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

// K8sPodExecutor is a type alias so existing ui code can continue using the unexported name.
type K8sPodExecutor = k8sprovider.K8sPodExecutor

// K8sNativeClient is a type alias so existing ui code can continue using the unexported name.
type K8sNativeClient = k8sprovider.K8sNativeClient

// DockerNativeClient is a type alias so existing ui code can continue using the unexported name.
type DockerNativeClient = dockerprovider.DockerNativeClient
