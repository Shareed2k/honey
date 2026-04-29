# hostctl

CLI to search **GCP Compute Engine**, **AWS EC2**, **Kubernetes** (nodes or pods), and **Consul** catalog nodes **in parallel**, optionally cache results, then use a **terminal UI** to SSH or open an **SSH local forward** (`-L`) via the system `ssh` binary.

## Prerequisites

- Go 1.22+
- Credentials for each backend you enable (see below)

After cloning, generate checksums:

```bash
cd hostctl
go mod tidy
```

## Build

```bash
go build -o hostctl ./cmd/hostctl
```

## Usage

```bash
# Interactive table (default); optional positional name is a substring filter
./hostctl search my-host

# JSON output, no TUI
./hostctl search --json my-host

# Limit providers
./hostctl search --provider aws,k8s web

# Regex filter
./hostctl search --name-regex '^prod-'

# Cache (default TTL 1m); force refresh
./hostctl search --refresh foo
./hostctl search --no-cache foo
./hostctl search --cache-ttl 5m foo
```

### TUI keys

- **Enter**: `ssh <user>@<ip>` (user from `--ssh-user`, default `$USER`)
- **t**: enter `-L` spec (e.g. `8080:localhost:8080`), then **Enter** to run `ssh -L ... user@ip`
- **q** / **Ctrl+C**: quit without SSH

### Provider auth / flags

| Provider | Auth / config |
|----------|----------------|
| **GCP** | Application Default Credentials; set `GOOGLE_CLOUD_PROJECT` or `GCP_PROJECT`, or pass `--gcp-project`. Optional `--gcp-zone` (default: all zones, aggregated list). |
| **AWS** | Default credential chain; `--aws-profile`, `--aws-region`. |
| **Kubernetes** | Current kubeconfig; `--kube-context`, `--kubeconfig`, `--k8s-mode=nodes` (default) or `pods`. |
| **Consul** | `CONSUL_HTTP_ADDR` or `--consul-addr`; `--consul-datacenter`, `--consul-token` / `CONSUL_HTTP_TOKEN`. |

If a provider is unreachable, the command fails (use `--provider` to narrow scope).

## Layout

- `cmd/hostctl` — entrypoint
- `internal/cli` — Cobra flags and wiring
- `internal/hosts` — `Record`, `Query`, cache, parallel orchestration
- `internal/provider/*` — GCP, AWS, k8s, Consul integrations
- `internal/ui` — Bubble Tea table + SSH actions

## Tests

```bash
go test ./...
```
