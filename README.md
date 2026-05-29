# honey

> **Alpha software** — APIs, config schema, and CLI flags may change between releases without notice.

CLI to search **GCP**, **AWS**, **Kubernetes**, **Docker**, **Consul**, **Proxmox**, and **TrueNAS** instances in parallel, then SSH, `docker exec`, or run recipes against results via a TUI, web UI, or MCP server.

## Prerequisites

- Go 1.26.2+ (for building from source)
- Credentials for each backend — see [Providers](https://shareed2k.github.io/honey/providers/)

## Install

**Homebrew (macOS):**

```bash
brew install --cask shareed2k/tap/honey
```

**Build from source:**

```bash
go build -o honey ./cmd/honey
```

## Quick start

```bash
# Interactive TUI — search all configured backends
honey search

# Filter by name substring
honey search my-host

# JSON output, AWS + Kubernetes only
honey search --json --provider aws,k8s web
```

## Documentation

| Feature | Guide |
|---------|-------|
| Providers (GCP, AWS, K8s, Consul, Proxmox, …) | [Providers](https://shareed2k.github.io/honey/providers/) |
| Docker & auto-discover on cloud VMs | [Docker auto-discover](https://shareed2k.github.io/honey/docker-auto-discover) |
| Macros (honeyfile) | [Macros](https://shareed2k.github.io/honey/macros) |
| MCP server (Cursor, LM Studio, OpenCode) | [MCP Server](https://shareed2k.github.io/honey/mcp-server) |
| Session recordings | [Recordings](https://shareed2k.github.io/honey/recordings) |
| Web UI & AI assist | [Web UI](https://shareed2k.github.io/honey/web-ui) |
| CUE recipes | [CUE Recipes](https://shareed2k.github.io/honey/cue-recipes) |
| Anomaly detection | [Anomaly Detection](https://shareed2k.github.io/honey/anomaly-detection) |
| Plugin development | [Plugins](https://shareed2k.github.io/honey/plugins-development) |
| Add a new backend | [Add new backend](https://shareed2k.github.io/honey/add-new-backend) |

Full docs: [shareed2k.github.io/honey](https://shareed2k.github.io/honey/)

## License

[MIT](LICENSE)
