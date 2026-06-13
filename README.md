![honey logo](honey_logo_single.png)

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
| Run the web UI in Docker (auth token) | [contrib/docker/README.md](contrib/docker/README.md) |
| CUE recipes | [CUE Recipes](https://shareed2k.github.io/honey/cue-recipes) |
| Anomaly detection | [Anomaly Detection](https://shareed2k.github.io/honey/anomaly-detection) |
| Plugin development | [Plugins](https://shareed2k.github.io/honey/plugins-development) |
| Add a new backend | [Add new backend](https://shareed2k.github.io/honey/add-new-backend) |

Full docs: [shareed2k.github.io/honey](https://shareed2k.github.io/honey/)

## Credits & Licenses

This project leverages state-of-the-art research algorithms to deliver real-time, context-aware operational intelligence:

1.  **LogLSHD Algorithm** (Proposed by Shu-Wei Huang et al.):
    - **Description**: Locality-Sensitive Hashing with Sequence-Alignment Clustering used for real-time log template mining.
    - **License**: [Creative Commons Attribution 4.0 International (CC BY 4.0)](https://creativecommons.org/licenses/by/4.0/).
    - **Citations**: *Huang, S.W., et al. "LogLSHD: Real-Time Log Template Mining via Locality-Sensitive Hashing and Dynamic Time Warping."*

2.  **LLMLog Algorithm** (Proposed by Fei Teng, Haoyang Li, and Lei Chen):
    - **Description**: Greedy set-cover adaptive demonstration selection (Algorithm 3) used for contextual few-shot prompt assembly.
    - **License**: [Creative Commons Attribution-NonCommercial-NoDerivatives 4.0 International (CC BY-NC-ND 4.0)](https://creativecommons.org/licenses/by-nc-nd/4.0/).
    - **Citations**: *Teng, F., Li, H., & Chen, L. "LLMLog: Advanced Log Template Generation via LLM-driven Multi-Round Annotation" (Proceedings of the VLDB Endowment, VLDB 2025).*

3.  **CoLA Two-Tier Pre-Screening** (Proposed by Tang et al.):
    - **Description**: Model collaboration pipeline filtering obvious logs via fast prescreeners to optimize LLM performance and reduce costs.
    - **License**: [Creative Commons Attribution 4.0 International (CC BY 4.0)](https://creativecommons.org/licenses/by/4.0/).
    - **Citations**: *Tang, et al. "CoLA: Model Collaboration for Log-based Anomaly Detection" (Proceedings of the VLDB Endowment, VLDB 2025).*

## License

[MIT](LICENSE)
