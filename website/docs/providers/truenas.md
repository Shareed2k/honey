---
id: providers-truenas
title: TrueNAS SCALE
slug: /providers/truenas
sidebar_position: 55
---

# TrueNAS SCALE

## Overview

Discovers resources on **TrueNAS SCALE 25.04+** via the WebSocket JSON-RPC API (`/api/current`):

| `meta.kind` | Source | Connectable via |
|-------------|--------|-----------------|
| `appliance` | Controller (`system.info`) | SSH to management URL host |
| `vm` | KVM guests (`vm.query`) | SSH when IP known (often not listed) |
| `virt_instance` | Incus/Virt instances (`virt.instance.query`) | SSH when alias IP present |

Only **RUNNING** guests are listed. Stopped VMs/instances are omitted.

## Minimal auth

- **TrueNAS SCALE 25.04 or newer** (legacy REST/WebSocket paths are not supported).
- **API key** created in the UI: **Credentials → API Keys**.
- Key is tied to a user; honey sends **`username`** (default `root`) with **`api_key`** via `auth.login_ex`.

Environment fallback: `TRUENAS_API_KEY`.

## Config (YAML)

Example file: [examples/config/truenas.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/truenas.yaml)

```yaml
backends:
  truenas:
    - name: lab-nas
      url: https://truenas.example.com    # required; normalized to wss://…/api/current
      api_key: "1-REPLACE_ME"             # required; secret
      username: root                      # optional — API key owner
      insecure: false                     # optional — skip TLS verify
      include_appliance: true             # optional; default true
      include_vms: true                   # optional; default true
      include_virt: true                  # optional; default true
      ssh_user: root                      # optional — meta hint for appliance SSH
```

## CLI (no config file)

| Flag | Purpose |
|------|---------|
| `--truenas-url` | Controller URL |
| `--truenas-api-key` | API key (or `TRUENAS_API_KEY`) |
| `--truenas-user` | API key username (default `root`) |
| `--truenas-insecure` | Skip TLS verification |

## Verify

```bash
export TRUENAS_API_KEY='1-…'
honey search --provider truenas --truenas-url https://nas.example.com -o json
```

## Notes

- **Virt/LXC-style instances** on SCALE appear under `virt_instance`, not `vm`.
- **KVM VMs** use `vm.query`; they may have no `primary_ip` until guest networking is visible to TrueNAS.
- Execution is **SSH only** in v1 (no TrueNAS API shell executor).
