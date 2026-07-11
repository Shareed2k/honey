---
id: providers-index
title: Providers
slug: /providers
sidebar_position: 1
---

# Providers

Honey discovers hosts from several backends in parallel. Each page below covers **minimal authentication**, a **minimal YAML** block, and **CLI flags** when you run without a config file.

After discovery, all host records can be enriched with global, group, and host-specific variables. See the [Inventory Variables](../inventory.md) guide for details on dynamic grouping and precedence.

Use `honey search --provider <id>` to limit to one type. YAML backend lists use the **`backends.<kind>`** key shown on each page.

| Provider | Search ID | Doc |
|----------|-----------|-----|
| Google Cloud | `gcp` | [GCP](/providers/gcp) |
| AWS | `aws` | [AWS](/providers/aws) |
| Kubernetes | `k8s` | [Kubernetes](/providers/kubernetes) |
| Consul | `consul` | [Consul](/providers/consul) |
| Proxmox VE | `proxmox` | [Proxmox](/providers/proxmox) |
| TrueNAS SCALE | `truenas` | [TrueNAS](/providers/truenas) |
| Static hosts | `local` | [Local](/providers/local) |
| Docker Engine | `docker` | [Docker](/providers/docker) |
| Remote honey server | `honey` | [Honey](/providers/honey) |

## Config file

Backends live under `backends:` in honey YAML (`version: 1`). Each entry needs a unique **`name`** when you use `--backends`. CLI query flags (for example `--gcp-project`) override per-backend YAML at search time.

See the main [documentation home](/) for config lookup order (`HONEY_CONFIG`, `~/.config/honey/config.yaml`, etc.).

Copy-paste samples: [examples/config/](https://github.com/shareed2k/honey/tree/main/examples/config) in the repository.
