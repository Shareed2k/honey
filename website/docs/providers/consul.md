---
id: providers-consul
title: Consul
slug: /providers/consul
sidebar_position: 40
---

# Consul

## Overview

Lists **catalog nodes** from a Consul cluster (service discovery). Rows use `provider: consul` with node name and address. Execution is via **SSH** to the node address when an IP is available.

## Minimal auth

- HTTP access to the Consul API (`addr`).
- **ACLs disabled:** no token required.
- **ACLs enabled:** a token with catalog read access (e.g. `node:read` on the default policy, or a read-only token).

Environment fallback: `CONSUL_HTTP_ADDR`, `CONSUL_HTTP_TOKEN`.

## Config (YAML)

Example file: [examples/config/consul.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/consul.yaml)

```yaml
backends:
  consul:
    - name: consul-prod
      addr: "http://10.0.0.5:8500"   # required
      datacenter: dc1                 # optional
      token: "secret-acl-token"       # optional; secret
```

Optional per-backend **`docker_discover`**.

## CLI (no config file)

| Flag | Purpose |
|------|---------|
| `--consul-addr` | `host:port` (or `CONSUL_HTTP_ADDR`) |
| `--consul-datacenter` | Datacenter |
| `--consul-token` | ACL token (or `CONSUL_HTTP_TOKEN`) |

## Verify

```bash
honey search --provider consul --consul-addr 127.0.0.1:8500 -o json
```

## Notes

- Node **meta** from Consul may be attached when the catalog provides it.
- Use HTTPS in `addr` when your agent expects TLS.
