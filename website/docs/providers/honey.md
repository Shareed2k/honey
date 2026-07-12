---
id: providers-honey
title: Honey (federation)
slug: /providers/honey
sidebar_position: 70
---

# Honey (federation)

## Overview

Proxies search and exec through a **remote honey server** instead of talking to a cloud/cluster API directly. Rows use `provider: honey`. Use this to federate a laptop's `honey` CLI through a central honey instance (e.g. behind a gateway) that itself has real GCP/AWS/k8s/etc. backends configured — you don't need those cloud credentials locally.

## Minimal auth

- **URL** of the remote honey server's API.
- **One of:**
  - **Bearer token** (`token`), or
  - **mTLS** — `mtls: true` routes the connection over the device mTLS client credential (see [`examples/mtls/apisix`](https://github.com/shareed2k/honey/tree/main/examples/mtls/apisix)) instead of the token.
  - **Mesh** — if the remote honey server sits behind NAT/CGNAT with no port-forward, `mesh: true` + `mesh_addr` route the connection through a libp2p mesh (Circuit Relay v2 + DCUtR hole-punching) instead of a direct TCP connection — see [`examples/mesh`](https://github.com/shareed2k/honey/tree/main/examples/mesh).

## Config (YAML)

```yaml
backends:
  honey:
    - name: central
      url: "https://honey.internal:8443"
      token: "secure:v1:..."   # or use mtls: true instead
      insecure: false           # skip TLS verify (self-signed certs)
```

| Field | Required |
|-------|----------|
| `name` | Yes |
| `url` | Yes |
| `token` | No — required unless `mtls: true` |
| `insecure` | No — skip TLS certificate verification |
| `mtls` | No — route over the device mTLS client credential instead of `token`; skipped locally if the device has no enrolled mTLS credential yet |
| `server_ca` | No — pin the gateway server certificate (PEM) the mTLS client trusts; empty falls back to the enrolled device CA |
| `mesh` | No — route this backend through the libp2p mesh instead of a direct connection; see the `mesh:` top-level config section below |
| `mesh_addr` | No — required when `mesh: true`; the libp2p multiaddr to dial, see [`examples/mesh`](https://github.com/shareed2k/honey/tree/main/examples/mesh) |

## Mesh (NAT traversal)

If the remote honey server has no public IP or port-forward (either side may be behind NAT/CGNAT), `mesh: true` on the backend dials it through a libp2p mesh instead of a direct TCP connection: Circuit Relay v2 gets the connection flowing through a relay, and DCUtR (Direct Connection Upgrade through Relay) hole-punches to a direct peer-to-peer connection when possible.

This process's own mesh identity is configured once, independent of any specific backend, in a top-level `mesh:` block:

```yaml
mesh:
  enabled: true
  private_key: "CAESQ..."      # go-libp2p identity key; see examples/mesh's "Generate a mesh identity key"
  relay_addrs:
    - "/ip4/203.0.113.10/udp/4001/quic-v1/p2p/12D3KooW..."
  # listen_mesh: false          # most instances leave this off
```

| Field | Required |
|-------|----------|
| `enabled` | Yes — turns this process's own mesh identity on |
| `private_key` | Yes, if `enabled` — this instance's libp2p identity key (base64, go-libp2p's "config file" key encoding); a secret, treat it like `token`/`server_ca` above |
| `relay_addrs` | Yes, if `enabled` — multiaddr(s) of the relay(s) this instance uses to obtain a relay reservation and dial through |
| `listen_mesh` | No — also run the Circuit Relay v2 relay service on this instance; only relevant if this instance is itself publicly reachable, so most instances leave it off |

The relay named in `relay_addrs` is a separate, generic, non-honey libp2p component — an operator self-hosts it (or points at any other correctly configured libp2p relay); honey does not ship or manage it.

See [`examples/mesh`](https://github.com/shareed2k/honey/tree/main/examples/mesh) for a full, runnable walkthrough: generating a mesh identity key, running an example relay, and configuring two honey instances to reach each other through it.

## CLI (no config file)

There are no dedicated `--honey-*` flags. Use a config file.

## Verify

```bash
honey search --provider honey --config ~/.config/honey/config.yaml -o json
```

## Notes

- **Backend name stripping:** if you query with `--backends <name-of-this-honey-backend>`, honey strips that name before forwarding to the remote server (the remote server only knows its own sub-backend names, not this proxy's local config name).
- **Exec proxying:** interactive actions (SSH-like exec) on rows from this provider are proxied through the remote honey server, not dialed directly from your machine.
- Rows carry `honey_upstream_backend` in `meta` so honey can route exec back through the correct configured `honey` backend.
