---
id: macros
title: Macros (honeyfile)
---

Honey macros let you define reusable named operations in a **honeyfile** manifest and run them with a single command. A honeyfile bundles common tasks — remote exec, recipe runs, log tailing, app proxy opens — so your team shares the same runbook without memorizing flags.

## Quick start

```bash
# Scaffold a starter honeyfile.yaml in the current directory
honey macros init

# List macros in the nearest honeyfile.yaml
honey macros --list

# Execute a macro
honey macros restart-nginx

# Dry-run: print resolved configuration without executing
honey macros restart-nginx --dry-run

# Use an explicit file
honey macros --file ops/honeyfile.yaml restart-nginx

# JSON output for --list / --dry-run
honey macros --list -o json
```

`honey macros init` scaffolds a `honeyfile.yaml` with one example macro of each kind (including `egress`, below). Flags: `--file` (output path), `--force` (overwrite an existing file). See [`honey macros init`](./cli/honey_macros_init.md).

The top-level **`-o`/`--output`** flag (`text` default, or `json`) controls `--list`/`--dry-run` output — this is distinct from the per-`exec`-macro `output` field described below.

CLI reference: [`honey macros`](./cli/honey_macros.md)

## Manifest format

A honeyfile uses Kubernetes-style YAML:

```yaml
apiVersion: honey.shareed2k.io/v1alpha1
kind: MacroSet
metadata:
  name: ops-macros
spec:
  macros:
    <macro-name>:
      kind: exec|recipe|logs|app|tunnel
      # ... kind-specific fields
```

Honey looks for `honeyfile.yaml` or `honeyfile.yml` in the current directory. Override with `--file <path>` or the `HONEY_MACROS_FILE` environment variable.

## Macro kinds

| Kind | What it does |
|------|-------------|
| `exec` | Run shell commands on matching hosts in parallel |
| `recipe` | Execute a CUE recipe file |
| `logs` | Stream logs from matching hosts |
| `app` | Open an HTTP app proxy in the browser |
| `tunnel` | Start a TCP proxy tunnel for an app |
| `egress` | Route local traffic through a honey-managed host (VPN-like exit) |

## exec

Runs one or more shell commands on hosts that match the target pattern.

```yaml
spec:
  macros:
    restart-nginx:
      kind: exec
      target: "web-*"
      command: "systemctl restart nginx"
      parallel: 50
      retry: 3
      timeout: 10s
      shell: auto
      runAs: root
      output: text
```

For multiple commands in sequence, use `commands` instead of `command`:

```yaml
    restart-and-check:
      kind: exec
      target: "web-*"
      commands:
        - "systemctl restart nginx"
        - "systemctl is-active nginx"
        - "ss -ltnp | grep :80"
      parallel: 20
      retry: 2
      timeout: 20s
```

**Fields:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `target` | string | — | Host name pattern (same syntax as `honey exec <target>`) |
| `command` | string | — | Single command to run |
| `commands` | list | — | Sequential commands (mutually exclusive with `command`) |
| `parallel` | int | 20 | Max concurrent executions |
| `retry` | int | 1 | Retry attempts per host (1 = no retry) |
| `timeout` | duration | 0 | Per-host timeout (e.g. `10s`, `2m`); 0 disables |
| `shell` | string | `auto` | Shell mode — see [Shell modes](#shell-modes) |
| `runAs` | string | — | Run via `sudo -n` as this user |
| `output` | string | `text` | Output format: `text` or `json` |
| `quiet` | bool | false | Suppress stdout, show status lines only |

## recipe

Executes a CUE recipe file. See [CUE Recipes](./cue-recipes.md) for recipe authoring.

```yaml
    deploy-staging:
      kind: recipe
      target: "web-*"
      provider: "gcp"
      backends: "gcp-stg2"
      recipePath: "examples/recipe/with_run_as.cue"
      execute: false
      env:
        - "APP_ENV=staging"
        - "IMAGE_TAG=latest"
```

**Fields:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `target` | string | — | Host name pattern |
| `recipePath` | string | — | Path to `.cue` recipe file |
| `execute` | bool | false | `false` = dry-run plan; `true` = execute |
| `env` | list | — | Extra environment variables (`KEY=value`) |

## logs

Streams logs from matching hosts. All options mirror `honey logs` flags.

```yaml
    tail-nginx:
      kind: logs
      target: "web-*"
      unit: "nginx"
      follow: true
      tail: 200
      since: 10m
      grep: "error|warn"
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `target` | string | Host name pattern |
| `source` | string | Log source: `journald`, `file`, `docker`, `cmd` |
| `unit` | string | systemd unit name |
| `file` | string | Path to log file (for `source: file`) |
| `cmd` | string | Command whose output to tail (for `source: cmd`) |
| `follow` | bool | Keep streaming (`-f`) |
| `tail` | int | Last N lines to show on connect |
| `since` | duration | Show logs since this duration ago |
| `grep` | string | Regex filter (grep before display) |
| `timestamps` | bool | Prefix lines with timestamps |
| `tui` | bool | Use terminal UI |
| `outputFile` | string | Write log output to this file |
| `maxConcurrency` | int | Max hosts to tail concurrently |
| `runAs` | string | Run log command as this user |

## app

Opens an HTTP app proxy session in the browser.

```yaml
    open-admin:
      kind: app
      app: "admin-ui"
      openBrowser: true
```

**Fields:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `app` | string | — | App name from `apps.*` in honey config |
| `openBrowser` | bool | true | Open the URL in the default browser |

## tunnel

Starts a TCP proxy tunnel for a configured app and keeps it running.

```yaml
    tunnel-postgres:
      kind: tunnel
      app: "postgres-tcp"
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `app` | string | App name from `apps.*` in honey config |

## egress

Routes local traffic through a honey-managed host — a VPN-like exit, built on the same tunnel/SOCKS machinery as recipe `tunnel:` steps (see [CUE Recipes — SOCKS](./cue-recipes.md#jump-host--many-internal-services-socks)).

```yaml
    corp-vpn:
      kind: egress
      host: "corp-bastion"
      port: 1080
      auto_proxy: true
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `host` | string | Single exit host (mutually exclusive with `hosts`) |
| `hosts` | list | Multiple candidate exit hosts |
| `port` | int | Local SOCKS/listen port |
| `bind` | string | Local bind address |
| `tun` | bool | Use a Layer-3 tun device instead of SOCKS |
| `auto_proxy` | bool | Automatically configure the OS/browser proxy to use this egress |
| `bypass` | list | Hosts/CIDRs to exclude from the proxy |

One of `host` or `hosts` is required.

## Host targeting

All exec/recipe/logs macros support these additional filter fields alongside `target`:

| Field | Description |
|-------|-------------|
| `provider` | Comma-separated provider IDs (e.g. `gcp,k8s`) |
| `backends` | Comma-separated backend names (e.g. `gcp-stg2,k8s-stg`) |
| `name` | Exact host name match |
| `nameRegex` | Go RE2 regex match on host name |

## Shell modes

| Mode | Behavior |
|------|----------|
| `auto` | Detects shell from remote environment; falls back to `sh` |
| `sh` | Force POSIX `/bin/sh` |
| `bash` | Force `/bin/bash` |
| `raw` | Direct execution — no shell wrapper |
| `powershell` | Windows PowerShell with job-based timeout |

## Full example

```yaml
apiVersion: honey.shareed2k.io/v1alpha1
kind: MacroSet
metadata:
  name: ops-macros
spec:
  macros:
    restart-nginx:
      kind: exec
      target: "web-*"
      provider: "gcp,k8s"
      backends: "gcp-stg2,k8s-stg"
      command: "systemctl restart nginx"
      parallel: 50
      retry: 3
      timeout: 10s
      shell: auto
      runAs: root

    restart-and-check-nginx:
      kind: exec
      target: "web-*"
      commands:
        - "systemctl restart nginx"
        - "systemctl is-active nginx"
        - "ss -ltnp | grep :80"
      parallel: 20
      retry: 2
      timeout: 20s

    tail-nginx:
      kind: logs
      target: "web-*"
      unit: "nginx"
      follow: true
      tail: 200
      since: 10m
      grep: "error|warn"

    deploy-recipe:
      kind: recipe
      target: "web-*"
      provider: "gcp"
      backends: "gcp-stg2"
      recipePath: "examples/recipe/with_run_as.cue"
      execute: false
      env:
        - "APP_ENV=staging"
        - "IMAGE_TAG=latest"

    open-admin-app:
      kind: app
      app: "admin-ui"
      openBrowser: true

    tunnel-postgres:
      kind: tunnel
      app: "postgres-tcp"
```

More examples in [`examples/macros/`](https://github.com/shareed2k/honey/tree/main/examples/macros) on GitHub.
