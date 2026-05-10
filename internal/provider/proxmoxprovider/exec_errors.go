package proxmoxprovider

import "errors"

var (
	errProxmoxUnknownKind      = errors.New("proxmox: unknown guest kind in meta (expected qemu or lxc)")
	errProxmoxNoInteractiveTTY = errors.New("proxmox: use the TUI or web terminal action; API RunInteractive is not used for LXC consoles")
	errProxmoxNoIP             = errors.New("proxmox: no primary IP for SSH tunnel fallback")
	errProxmoxLXCNoGuestIP     = errors.New("proxmox: LXC needs primary_ip for SSH (Proxmox VE has no REST exec for containers); web console still uses PVE termproxy when token_id is set")
	errProxmoxMeta             = errors.New("proxmox: missing node or vmid in search metadata")
	errProxmoxFileOps          = errors.New("proxmox: SFTP/file browser not available via PVE API alone; use exec_mode=hybrid (SSH for files) or exec_mode=ssh")
	errProxmoxStreams          = errors.New("proxmox: streaming stdin over PVE API exec is not supported")
)
