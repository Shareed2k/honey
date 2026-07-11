---
id: providers-docker
title: Docker
slug: /providers/docker
sidebar_position: 35
---

# Docker

## Overview

Lists **containers** and/or **Swarm tasks** from a Docker Engine API endpoint. Rows use `provider: docker` with `meta.kind` of `container` or `swarm_task`. Interactive use is **`docker exec`** / **`docker cp`**, not SSH into the container network.

## Minimal auth

Depends on how you reach the daemon:

| Mode | Minimal setup |
|------|----------------|
| **Local socket** | Membership in `docker` group (or root); default `DOCKER_HOST` / `/var/run/docker.sock` |
| **TCP** | Reachable `tcp://` endpoint and any daemon TLS/auth it requires |
| **Moby `ssh://`** | SSH access as configured in the URL (`host: ssh://user@host`) |
| **Honey SSH** | SSH to the VM (`via_local` / `via_ssh`) plus permission to dial `docker.sock` (often `sudo` + `run_as`) |

No honey-specific API keys.

## Config (YAML)

Example file: [examples/config/docker.yaml](https://github.com/shareed2k/honey/blob/main/examples/config/docker.yaml)

```yaml
backends:
  docker:
    - name: local
      host: ""                    # empty = DOCKER_HOST / local socket
      mode: containers            # containers | swarm | both
      all_containers: false
    - name: vm-docker
      via_local: lab              # backends.local[].name
      socket: /var/run/docker.sock
      run_as: root
      platform: linux
    - name: remote-ssh
      via_ssh:
        host: 10.0.0.1
        user: deploy
        identity_file: ~/.ssh/id_ed25519
      socket: /var/run/docker.sock
    - name: remote-tcp
      host: "tcp://10.0.0.2:2376"
      tls_verify: true
      ca_cert: /path/to/ca.pem
      cert: /path/to/cert.pem
      key: /path/to/key.pem
```

| Field | Required |
|-------|----------|
| `name` | Yes |
| `host`, `via_local`, `via_ssh`, `socket`, `mode`, `platform`, `run_as`, `all_containers` | Optional |
| `tls_verify`, `ca_cert`, `cert`, `key` | Optional — client TLS for `tcp://`/`https://` hosts (config-only, no CLI flags) |

## CLI (no config file)

| Flag | Purpose |
|------|---------|
| `--docker-host` | `unix://`, `tcp://`, `ssh://`, or empty |
| `--docker-mode` | `containers`, `swarm`, `both` |
| `--docker-all` | Include stopped containers |
| `--docker-via-local` | Honey SSH via named local backend |
| `--docker-via-ssh-host` | Explicit SSH host for Honey hop |
| `--docker-socket` | Remote socket path |
| `--docker-platform` | `linux` or `windows` |

## Verify

```bash
honey search --provider docker -o json
honey search --provider docker --docker-host unix:///var/run/docker.sock -o json
```

## Notes

- **Auto-discover on other backends** (a second pass over hosts from GCP, AWS, Consul, Local, or Proxmox — SSH in and list containers): experimental; see [Docker auto-discover](../docker-auto-discover). Requires `HONEY_FEATURE_DOCKER_VIA_PROVIDERS=1` and is not in YAML today.
- Moby **`ssh://`** does not use honey’s `~/.ssh/config` integration; use **`via_ssh`** for ProxyJump and honey SSH features.
