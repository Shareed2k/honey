---
id: providers-proxmox
title: Proxmox VE
slug: /providers/proxmox
sidebar_position: 50
---

# Proxmox VE

## Overview

Lists **QEMU VMs and LXC containers** on a Proxmox VE cluster. Rows use `provider: proxmox` with guest type in `meta.kind` (`qemu` or `lxc`). Default execution is **SSH** to guest IPs when discoverable; optional **`exec_mode`** (`ssh`, `pve`, `hybrid`) uses the Proxmox API for some operations.

## Minimal auth

- **API URL** to Proxmox (`https://host:8006/api2/json`).
- **One of:**
  - **User + password** (e.g. `root@pam`), or
  - **API token:** `token_id` (e.g. `root@pam!honey`) + `token_secret`.
- **Permissions:** read VM/container list and network info (e.g. **`PVEVMRO`** read-only role on `/vms`).

### Create an API token (minimal)

1. Proxmox UI → **Datacenter** → **Permissions** → **API Tokens** → **Add**.
2. User: `root@pam`, token name: `honey`.
3. Assign **`PVEVMRO`** (or disable privilege separation if you accept broader access).
4. Copy the secret; set `token_id` to `root@pam!honey` in YAML.

## Config (YAML)

Example file: [examples/config/proxmox.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/proxmox.yaml)

```yaml
backends:
  proxmox:
    - name: pve-home
      url: "https://10.0.0.10:8006/api2/json"   # required
      user: "root@pam"                           # or use token_id + token_secret
      password: "secret"
      # token_id: "root@pam!honey"
      # token_secret: "uuid-secret"
      insecure: true                             # optional — skip TLS verify
      exec_mode: ssh                             # ssh | pve | hybrid
```

Optional per-backend **`docker_discover`**.

## CLI (no config file)

| Flag | Purpose |
|------|---------|
| `--proxmox-url` | API base URL |
| `--proxmox-user` / `--proxmox-password` | Password auth |
| `--proxmox-token-id` / `--proxmox-token-secret` | Token auth |
| `--proxmox-insecure` | Skip TLS verification |

## Verify

```bash
honey search --provider proxmox \
  --proxmox-url https://10.0.0.10:8006/api2/json \
  --proxmox-token-id 'root@pam!honey' \
  --proxmox-token-secret 'YOUR_SECRET' \
  -o json
```

## Notes

- Guest IPs may require QEMU guest agent or LXC network data; guests without IPs appear but are not SSH-connectable.
- Self-signed PVE certificates often need `insecure: true` or a proper CA trust store.
