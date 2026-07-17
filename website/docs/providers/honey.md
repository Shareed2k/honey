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
| `enabled` | No — defaults to off; set `true` to turn this process's own mesh identity on |
| `private_key` | Yes, if `enabled` — this instance's libp2p identity key (base64, go-libp2p's "config file" key encoding); a secret, treat it like `token`/`server_ca` above |
| `relay_addrs` | Yes, if `enabled` — multiaddr(s) of the relay(s) this instance uses to obtain a relay reservation and dial through |
| `listen_mesh` | No — also run the Circuit Relay v2 relay service on this instance; only relevant if this instance is itself publicly reachable, so most instances leave it off |

The relay named in `relay_addrs` is a separate, generic, non-honey libp2p component — an operator self-hosts it (or points at any other correctly configured libp2p relay); honey does not ship or manage it.

See [`examples/mesh`](https://github.com/shareed2k/honey/tree/main/examples/mesh) for a full, runnable walkthrough: generating a mesh identity key, running an example relay, and configuring two honey instances to reach each other through it.

## Mobile app (Android) — no SSH private key on the phone

The Android app's `config.yaml` (edited from the Config tab, or pushed to `configDir/config.yaml`) is the exact same schema as above — a `backends.honey` entry, optionally combined with `mesh: true`/`mesh_addr` and the top-level `mesh:` block for NAT traversal. Once configured:

```yaml
backends:
  honey:
    - name: central
      url: "https://honey.internal:8443"
      mtls: true          # phone already enrolls a device mTLS credential
      mesh: true           # only if central sits behind NAT/CGNAT
      mesh_addr: "/ip4/203.0.113.10/udp/4001/quic-v1/p2p/12D3KooW..."

mesh:
  enabled: true
  private_key: "CAESQ..."
  relay_addrs:
    - "/ip4/203.0.113.10/udp/4001/quic-v1/p2p/12D3KooW..."
```

- **Exec** (run-command) against hosts returned by this backend proxies through `central` — the phone never needs an SSH private key for them. The app's SSH-key picker is replaced by a "Routed via provider: central (mTLS)" line for these hosts.
- **VPN/tunnel** works the same way: picking a `central`-backed exit host tunnels through the remote honey server's own SSH session to that host, again with no private key on the phone.
- Auth to `central` itself is mTLS (the phone's already-enrolled device credential) and/or the libp2p mesh — not a bearer token, since there's no good place to store one long-term on a phone.
- A plain SSH username is still sent (it's not a secret) — the remote honey server uses it to open its own SSH session to the target host.

## Tunneling & VPN over the proxy (interactive, forwards, UDP, tun)

Interactive terminal, local forward, dynamic forward (SOCKS5), remote
forward, UDP relay, and TUN/VPN mode all work the same way against a
`honey`-backend host as they do against a direct SSH host — the remote honey
server does the actual dial (SSH or otherwise) to the target; the client
only talks to it over the backend's own auth (token, mTLS, and/or mesh).
Concretely:

- `honey <host>` (interactive shell), `honey proxy tcp` / local forwards,
  `honey egress <host>` (SOCKS5 dynamic forward), remote forwards, and UDP
  relays are all executed **server-side** and relayed back over the proxy
  connection.
- `honey egress <host> --tun` composes the same dynamic-forward mechanism
  with a local `tun2proxy` process for a transparent VPN exit.
- **No SSH private key is ever needed on the client** for these hosts — see
  ["Mobile app"](#mobile-app-android--no-ssh-private-key-on-the-phone) above,
  which applies equally to the desktop CLI.

### UDP relay: two modes

UDP relay has two modes, selected by `useSocat` (a recipe's `tunnel.remote_socat` field, see [CUE recipes](/cue-recipes)):

- **`remote_socat: true` — target-vantage.** The remote honey server starts a `socat TCP-LISTEN ... UDP:...` relay **on the target** (via its own exec path to that host) and bridges local UDP flows to it over the existing proxy connection. Requires `socat` to be installed on the target; traffic to the real UDP destination originates from the target's own network vantage point.
- **`remote_socat: false` — server-vantage.** The **honey server itself** dials `remoteHost:remotePort` over a dedicated `/api/v1/ws/udp` WebSocket bridge (a pure-Go relay, no external tooling required anywhere) and shuttles length-prefixed UDP datagram frames back to the client. No dependency on the target having `socat` — the server reaches whatever *it* can route to, which includes hosts only the server has network access to (e.g. through a server-side VPN or the honey mesh), not just what's reachable from the target itself.

Both modes are gated by the same token/mTLS auth as everything else in this doc. **`remote_socat: false` lets an authorized caller make the honey server originate UDP traffic to an arbitrary `host:port`** — this is an SSRF-shaped surface, so it is additionally gated by [OPA policy](/authorization) the same way the other privileged proxy actions (like remote forward, below) are; make sure that policy actually covers this action if you rely on it.

Two operational caveats:

- **Remote forward is a reverse exposure.** Unlike the other modes, a
  remote forward makes the **server** open a listener reachable from the
  target-side network — it's an inbound exposure on the server, not just an
  outbound relay on your behalf. It's gated by the same token/mTLS auth as
  everything else in this doc, plus [OPA policy](/authorization) if one is
  configured — make sure that policy actually covers this action if you're
  relying on it.
- **TUN mode needs local root + the `tun2proxy` binary.** The VPN exit
  shells out to `tun2proxy` on the machine running the CLI; creating/routing
  the TUN device needs root there, independent of the remote honey server.

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
