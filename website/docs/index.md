---
id: index
title: Honey Documentation
slug: /
---

Search **GCP**, **AWS**, **Kubernetes**, **Docker**, **Consul**, **Proxmox**, **TrueNAS**, static **local** hosts, and other **remote honey servers** in parallel, then SSH, `docker exec`, or run recipes against results via a TUI, web UI, or MCP server.

## Install

```bash
brew install --cask shareed2k/tap/honey
```

Or build from source:

```bash
go build -o honey ./cmd/honey
```

## Quick start

```bash
# Interactive TUI — search all configured backends
honey search

# Filter by name substring
honey search my-host

# JSON output
honey search --json --provider aws,k8s web
```

## Guides

- [Providers](./providers) — GCP, AWS, Kubernetes, Consul, Proxmox, TrueNAS, local, Docker
- [Docker auto-discover on cloud VMs](./docker-auto-discover.md)
- [Macros (honeyfile)](./macros.md) — reusable named operations for exec, recipes, logs, and app/tunnel tasks
- [Inventory Variables](./inventory.md) — group and host vars for dynamic providers and recipes
- [MCP Server](./mcp-server.md) — use honey as an MCP tool server in Cursor, LM Studio, and OpenCode
- [Plugins](./plugins.md) — install and configure WASM plugins; extend CUE recipes with custom steps
- [Session Recordings](./recordings.md) — list, search, export, replay, and prune terminal session recordings
- [Web UI & AI assist](./web-ui.md)
- [CUE recipes](./cue-recipes.md)
- [Anomaly Detection](./anomaly-detection.md)
- [Plugin development](./plugins-development.md)
- [Vulnerability & patch management](./vulnerability-management.md)
- [Authorization](./authorization.md) — RBAC/OPA policy for exec, recipes, and the web UI
- [Command Risk Engine](./command-risk.md) — deterministic risk analysis before a command runs
- [Alert Investigation](./alert.md) — Alertmanager webhook receiver with auto-investigate
- [Distributed Tracing](./tracing.md)
- [Add a new backend](./add-new-backend.md)
