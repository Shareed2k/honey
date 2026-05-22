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
| `appliance` | Controller (`system.info`) | SSH to management URL host; **API shell** (Web UI) |
| `vm` | KVM guests (`vm.query`) | SSH when IP known; **VM console** (API) |
| `virt_instance` | Incus/Virt instances (`virt.instance.query`) | SSH when alias IP present; **Container shell** (API) |

Only **RUNNING** guests are listed. Stopped VMs/instances are omitted.

## Minimal auth

- **TrueNAS SCALE 25.04 or newer** (legacy REST/WebSocket paths are not supported).
- Use **`https://`** in `url` (not `http://`). TrueNAS requires TLS for API keys; honey dials **`wss://`** and uses `insecure: true` only to **skip certificate verification** (self-signed certs), not to use plaintext HTTP.
- **API key** created in the UI: **Credentials → API Keys**.
- Copy the **full** key string from TrueNAS (`<numeric-id>-<secret>`, e.g. `1-…`). Do not paste only the secret portion.
- **`username`** must be the user that **owns** the API key (default `root` only if the key was created for root).
- Keys may have an **expiry**; expired keys return an auth error — create a new key or extend expiry in the UI.
- Honey sends `username` + `api_key` via `auth.login_ex` (`API_KEY_PLAIN`).

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

## Troubleshooting

| Error / symptom | What to check |
|-----------------|---------------|
| `invalid API key or username` | Full `id-secret` key, correct `username`, key not revoked |
| AUTH_ERR after `http://` URL | Use **`https://`** + `insecure: true`; create a **new** API key (TrueNAS may revoke keys sent over HTTP) |
| `API key or credential expired` | Create a new API key in **Credentials → API Keys** |
| `two-factor authentication required` | Use an API key for a user without 2FA (OTP login is not supported in honey v1) |
| Empty key after Web UI edit | Re-enter **API key** in Add/Edit — leaving the secret field blank overwrites the saved value |

## Web UI terminals

Honey can open interactive shells through TrueNAS **`/websocket/shell`** (server-side bridge; API keys stay on the honey host):

| Web UI action | Record kind | TrueNAS behavior |
|---------------|-------------|------------------|
| **API shell** | `appliance` | Host login shell on the controller |
| **VM console** | `vm` | Incus console via `virt_instance_id` (resolved from `virt.instance` by name when possible) |
| **Container shell** | `virt_instance` | Incus exec via `virt_instance_id` (`meta.id`) |

SCALE 25.04+ [`webshell_app.py`](https://github.com/truenas/middleware/blob/master/src/middlewared/middlewared/apps/webshell_app.py) uses **`virt_instance_id`** for Incus guests. Legacy keys such as `container_id` or `vm_id` are ignored and open the **host** shell instead.

The usual **Terminal** button still uses **SSH** when `primary_ip` is set. API shells require the API key user to have **Web Shell** (and related) privileges on TrueNAS; otherwise the UI shows the middleware error (for example “Web shell privilege not granted”).

When the honey web server host has **tmux** or **zellij**, API shell tabs support the same session reattach on browser refresh as SSH terminals (local pty-proxy). Closing a tab with the **×** button ends the background session; refreshing the page reattaches while the tab stays open. The modal **Close** button only hides the window; use **×** on each tab to end sessions, or the floating **Open Terminals** button to reopen.

In the **`honey search` TUI**, **Enter** on a TrueNAS row opens the API shell (same targets as the web API shell button). Press **`s`** on a TrueNAS row with `primary_ip` set to SSH to the management IP (same as the web **Terminal** button). Parallel command (**`e`**) and CUE recipes (**`r`**) run commands through the API shell on TrueNAS rows (not SSH). With **`kv_tunnel`**, an **appliance** with `primary_ip` prefers an SSH remote-forward to stepkv when SSH dial succeeds; if SSH is unavailable, honey falls back to the same in-shell Python bridge used for guests. **Virt instances** and **VMs** always use that API-shell bridge (same `HONEY_KV_URL` / `HONEY_KV_TOKEN` env vars as other providers).

Protocol reference: [truenas/middleware `webshell_app.py`](https://github.com/truenas/middleware/blob/master/src/middlewared/middlewared/apps/webshell_app.py).

## Port-forward (Tunnel)

The **Tunnel** action (TUI **`t`**, web **Tunnel** button, **Tunnels** tab) maps a local port to a remote `host:port` the same way as SSH `-L`:

| Row | Transport |
|-----|-----------|
| **Appliance** (or any row) with `primary_ip` | SSH to the management IP; remote host is resolved **from the NAS** (e.g. `8080:127.0.0.1:80` reaches a service on the controller). |
| **Virt instance** / **VM** without `primary_ip` | **API shell** plus an in-guest Python TCP dial bridge: honey listens on `127.0.0.1:<local>` and each connection is dialed inside the guest to `remoteHost:remotePort` (e.g. `8080:127.0.0.1:443` for a service in the container). |

The API-shell tunnel path requires **`python3`** in the guest. Mapping format is `localPort:remoteHost:remotePort` (same as other providers).

## Notes

- **Virt/LXC-style instances** on SCALE appear under `virt_instance`, not `vm`.
- **KVM VMs** use `vm.query`; they may have no `primary_ip` until guest networking is visible to TrueNAS — use **VM console** when SSH is unavailable (requires a matching `virt.instance` for Incus console).
- **`app_name` + `container_id`** on `/websocket/shell` is for TrueNAS **apps** (Docker), not `virt_instance` rows from search.
- `auth.generate_token` may be disabled on systems with strict STIG / replay-resistant auth ([API docs](https://api.truenas.com/v25.10/api_methods_auth.generate_token.html)).
