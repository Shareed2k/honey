---
id: providers-local
title: Local (static hosts)
slug: /providers/local
sidebar_position: 60
---

# Local (static hosts)

## Overview

Lists a **fixed set of hosts** you define in YAML. No cloud or cluster API is called. Rows use `provider: local` with the names and IPs you configure. Execution is via **SSH** to `primary_ip`.

## Minimal auth

- **None** for discovery (static data only).
- **SSH** to each host still requires your normal keys, `~/.ssh/config`, or `ssh-agent` when you connect from honey.

## Config (YAML)

Example file: [examples/config/local.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/local.yaml)

```yaml
backends:
  local:
    - name: lab
      hosts:
        - name: web1
          primary_ip: 10.0.0.10
          zone: lab
          region: on-prem
          meta:
            role: web
        - name: db1
          primary_ip: 10.0.0.11
```

| Field | Required |
|-------|----------|
| `name` (backend) | Yes |
| `hosts[].name` | Yes |
| `hosts[].primary_ip` | Yes |
| `hosts[].extra_ips`, `zone`, `region`, `meta`, `ssh_user` | No |

Optional per-backend **`docker_discover`** (SSH to each host and list containers).

## CLI (no config file)

There are no dedicated `--local-*` flags. Use a config file, or combine **local** backends in YAML with flag-only providers.

Implicit default backend: empty `local` list means this provider contributes nothing unless configured.

## Verify

```bash
honey search --provider local --config ~/.config/honey/config.yaml -o json
```

## Notes

- Use **local** for bare metal, appliances, or any host not exposed by another provider.
- **Docker via SSH:** `backends.docker[].via_local` can reference a `backends.local[].name` for Engine API over SSH.
