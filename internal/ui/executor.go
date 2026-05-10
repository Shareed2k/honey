package ui

import (
	"fmt"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// HostClient aliases the shared execution interface (see internal/hostexec).
type HostClient = hostexec.HostClient

// Executor aliases the shared executor interface.
type Executor = hostexec.Executor

// RemoteFileEntry aliases remote file metadata for JSON APIs.
type RemoteFileEntry = hostexec.RemoteFileEntry

// GetExecutor returns the appropriate Executor for a host record.
func GetExecutor(r hosts.Record) hostexec.Executor {
	return hostexec.ForRecord(r)
}

// FormatTargetForDryRun returns a string describing how the target will be connected to.
func FormatTargetForDryRun(r hosts.Record) string {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return fmt.Sprintf("k8s_exec(ns=%s pod=%s)", r.Meta["namespace"], r.Meta["pod_name"])
	}
	if r.Provider == "proxmox" {
		if b, ok := hostexec.ProxmoxBackendByName(r.Meta["backend_name"]); ok {
			switch b.ExecMode {
			case hostexec.ProxmoxExecPVE:
				return fmt.Sprintf("proxmox_pve(node=%s vmid=%s kind=%s)", r.Meta["node"], r.Meta["vmid"], r.Meta["kind"])
			case hostexec.ProxmoxExecHybrid:
				return fmt.Sprintf("proxmox_hybrid(node=%s vmid=%s kind=%s ip=%s)", r.Meta["node"], r.Meta["vmid"], r.Meta["kind"], r.PrimaryIP)
			}
		}
	}
	return fmt.Sprintf("ip=%s", r.PrimaryIP)
}

type k8sPodExecutor struct{}
