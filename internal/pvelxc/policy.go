package pvelxc

import (
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
)

// ShouldUsePVETTY reports whether the Proxmox guest should use the PVE serial/text console
// (termproxy + vncwebsocket framing): proxmox LXC or QEMU row, pve or hybrid exec_mode, API token, node+vmid.
func ShouldUsePVETTY(rec hosts.Record) bool {
	if rec.Provider != "proxmox" {
		return false
	}
	k := strings.ToLower(strings.TrimSpace(rec.Meta["kind"]))
	if k != "lxc" && k != "qemu" {
		return false
	}
	if strings.TrimSpace(rec.Meta["node"]) == "" || strings.TrimSpace(rec.Meta["vmid"]) == "" {
		return false
	}
	b, ok := proxmoxprovider.BackendByName(rec.Meta["backend_name"])
	if !ok {
		return false
	}
	switch b.ExecMode {
	case proxmoxprovider.ProxmoxExecPVE, proxmoxprovider.ProxmoxExecHybrid:
	default:
		return false
	}
	return strings.TrimSpace(b.TokenID) != ""
}

// ShouldUsePVEQemuWebVNC is true when the record may open the Proxmox QEMU graphics console (vncproxy + vncwebsocket).
// Web-only; TUI uses ShouldUsePVETTY (serial) instead.
func ShouldUsePVEQemuWebVNC(rec hosts.Record) bool {
	if !ShouldUsePVETTY(rec) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(rec.Meta["kind"]), "qemu")
}
