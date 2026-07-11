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
