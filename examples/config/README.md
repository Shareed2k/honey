# Honey config examples

Minimal `backends.*` samples—one file per provider. Copy, replace placeholders, and point honey at a file:

```bash
honey search --config examples/config/gcp.yaml --provider gcp -o json
```

Or set `HONEY_CONFIG` to a file path.

| File | Provider | Docs |
|------|----------|------|
| [gcp.yaml](gcp.yaml) | `gcp` | [GCP setup](../../website/docs/providers/gcp.md) |
| [aws.yaml](aws.yaml) | `aws` | [AWS setup](../../website/docs/providers/aws.md) |
| [kubernetes.yaml](kubernetes.yaml) | `k8s` | [Kubernetes setup](../../website/docs/providers/kubernetes.md) |
| [consul.yaml](consul.yaml) | `consul` | [Consul setup](../../website/docs/providers/consul.md) |
| [proxmox.yaml](proxmox.yaml) | `proxmox` | [Proxmox setup](../../website/docs/providers/proxmox.md) |
| [truenas.yaml](truenas.yaml) | `truenas` | [TrueNAS setup](../../website/docs/providers/truenas.md) |
| [local.yaml](local.yaml) | `local` | [Local setup](../../website/docs/providers/local.md) |
| [docker.yaml](docker.yaml) | `docker` | [Docker setup](../../website/docs/providers/docker.md) |

Published provider guides: [Providers](https://shareed2k.github.io/honey/providers/).

Regenerate CLI reference pages (same as CI):

```bash
go run ./cmd/honey docs ./website/docs/cli
```
