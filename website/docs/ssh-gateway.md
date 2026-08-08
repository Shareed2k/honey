---
id: ssh-gateway
title: SSH Gateway
slug: /ssh-gateway
---

`honey ssh-server` runs an **inbound SSH gateway**: users connect with a native
`ssh` client, authenticate with an **SSH certificate**, and honey proxies the
session to a host from your [inventory](./inventory.md) — recorded, policy-gated,
and audited. It is honey's downstream SSH machinery (host dialing, PTY,
recording, [OPA policy](./authorization.md), [command-risk](./command-risk.md),
[audit](#recording--audit)) turned around into a front door, so the gateway adds
only the inbound server, certificate auth, and resource routing.

Users never receive per-host keys; the gateway holds the connection to the
target and the operator issues short-lived user certificates from honey's own SSH
CA. Every session is subject to the same gates as the rest of honey.

## Quick start

```bash
# 1. Create the gateway's SSH CA (once). Prints the CA public key.
honey ssh-ca init

# 2. Issue a short-lived cert for a user's public key.
honey ssh-ca sign --pubkey alice.pub --principal alice --ttl 1h
#   -> writes alice-cert.pub

# 3. Run the gateway. It auto-trusts the CA created by `ssh-ca init`.
honey ssh-server --listen :12222

# 4. Connect with a native ssh client. The resource is an inventory host name.
ssh -t -i alice -i alice-cert.pub alice@gateway-host -p 12222 <resource>          # interactive shell
ssh    -i alice -i alice-cert.pub alice@gateway-host -p 12222 <resource> uptime    # ad-hoc command
echo 'SELECT now()' | ssh -i alice -i alice-cert.pub alice@gateway-host -p 12222 <resource> psql   # stdin pipe
```

`<resource>` is the first argument of the ssh command and names a host from the
same inventory `honey search` uses (literal name; an IP is accepted too). With a
trailing command the gateway runs it non-interactively; with no command and a
PTY (`-t`) it opens an interactive shell.

The **target login user** is resolved from the record's `ssh_user` meta, then
`defaults.ssh_user`, then the certificate principal — the principal is the
*authorization* identity, not necessarily the account on the target.

## Target types

`<resource>` may be any connectable record from the inventory; the gateway routes
it through the same executor seam the web terminal uses:

| Target | Interactive shell (`-t`) | Ad-hoc exec | `ssh -L` |
| --- | --- | --- | --- |
| SSH host | ✓ | ✓ | ✓ |
| Docker container | ✓ (container exec) | ✓ | ✓ (via the container) |
| Kubernetes pod | ✓ (ephemeral debug container) | ✓ | ✓ (SPDY port-forward) |
| honey-mesh record | ✓ (forwarded to the owning node) | ✓ | ✓ |
| Proxmox serial / TrueNAS shell | ✓ (provider console) | — | — |

k8s pods use an **ephemeral debug container** (needs k8s ≥1.25 + RBAC to create
`pods/ephemeralcontainers`) since the pod's own image often has no shell. Proxmox
and TrueNAS records are **console-only**: an interactive shell works, but exec and
port-forward are rejected (a serial/shell console is neither a command channel nor
a TCP endpoint). All target types are recorded, masked, guarded, and OPA-gated
identically.

## Certificate authentication

The gateway accepts only SSH **certificates** signed by a trusted CA (plain
public keys are rejected). `ssh.CertChecker` verifies the signature, the
`user` certificate type, the validity window, and that the ssh login name is a
listed principal. The honey **actor** is then derived from the certificate:

- `--cert-attr principal` (default) — the validated login principal.
- `--cert-attr key_id` — the certificate's key id.

`--user-attr` is a label recorded alongside the actor for audit. It is
**deny-by-default**: with no trusted CA configured (and without `--no-auth`) the
gateway refuses to start.

Trust is configured in priority order: `--trusted-ca <file>` (repeatable) →
`ssh_gateway.trusted_ca` in config → the built-in CA from `honey ssh-ca init`
(auto-trusted from the state dir). `--no-auth` disables authentication entirely
and is for local development only.

### Issuing certificates

```bash
honey ssh-ca init                 # create + print the CA public key
honey ssh-ca print-ca             # print it again (for --trusted-ca / config)
honey ssh-ca sign --pubkey user.pub --principal alice --principal ops \
  --key-id alice --ttl 1h --out alice-cert.pub
```

Certificates carry the `permit-pty` and `permit-port-forwarding` extensions.
Keep the TTL short and re-issue — that is the point of a CA over distributing
keys.

### Self-service enrollment

For hands-off issuance, an operator mints a one-time code and the user redeems it
with their public key (no key distribution, no shared secret beyond the code).
The endpoints live on `honey web`:

```bash
# Operator (authenticated): mint a one-time code.
honey ssh-ca enroll-code --principal alice --ttl 1h
#   POST /api/v1/ssh/enroll-code -> {code, expires_in_seconds, ca}

# User: generate a key and redeem the code.
ssh-keygen -t ed25519 -f id_honey -N ''
curl -sS -X POST https://honey-web/api/v1/ssh/enroll \
  -d "{\"code\":\"<code>\",\"public_key\":\"$(cat id_honey.pub)\"}"
#   -> {cert, ca, principals, valid_before_unix}   (save cert as id_honey-cert.pub)

ssh -i id_honey -i id_honey-cert.pub alice@gateway-host -p 12222 <resource>
```

The code is single-use, expires in 10 minutes, and the granted principals come
from the operator (the redeemer cannot escalate). `/api/v1/ssh/enroll-code` is
authenticated; `/api/v1/ssh/enroll` is authorized by the code itself.

## Port-forwarding

`ssh -L` reaches a service on a resolved host's loopback — e.g. a database bound
to `127.0.0.1` on the target:

```bash
ssh -N -L 15432:<resource>:5432 alice@gateway-host -p 12222
# then connect a client to localhost:15432
```

The forward's destination host is the inventory resource; the gateway SSH-dials
it and connects to `127.0.0.1:<port>` on that host. Each forward is authorized by
the OPA `tunnel` action (same input shape as the [web tunnel](./web-ui.md) gate)
before any connection is made.

## Recording & audit

Sessions are recorded to `.hrec.jsonl` under the record dir when
`ssh_gateway.record` is set (or `--record-dir` / `defaults.record_dir`), with
`trigger=ssh-gateway`, so they appear in **`honey recordings`** (list, play,
grep, stats, export to asciinema — see [Session recordings](./recordings.md)).
Every decision (session open, command, exit, tunnel, denials) is written to the
[audit log](./authorization.md) with `source=ssh-gateway`, visible via
**`honey audit tail`** / **`honey audit export`**.

## Policy gates

The gateway reuses honey's shared gate, so one policy governs the web UI, MCP, the
recipe engine, and the gateway:

- **`interactive_session`** — opening an interactive shell (`{actor, target}`).
- **`command_exec`** — an ad-hoc command (`{actor, command, target}`), combined
  with the deterministic [command-risk](./command-risk.md) floor (a `critical`
  signal denies even with no policy).
- **`tunnel`** — a `ssh -L` destination (`{actor, target:{scheme,host,port}}`).

Point the gateway at a policy directory with `ssh_gateway.policy_dir` (or the
shared `HONEY_POLICY_DIR`). A nil policy allows (subject to the command-risk
floor).

## Data masking

Redact secrets from a session's **output** — both the live stream and the
recording — with literal values and/or regular expressions:

```yaml
ssh_gateway:
  mask:
    values: ["s3cr3t-token"]            # exact strings
    patterns: ["AKIA[0-9A-Z]{16}"]      # RE2 regexes
```

Matches are replaced with `[MASKED]`. The redactor is streaming and holds back
only a tiny lookback so interactive output (prompts, recent lines) is not
delayed, and it never flushes through a partial secret. **Limits:** masking is
pattern/value based — it cannot redact a value it was not told about; a regex
match longer than the lookback split across two reads, or a secret split at the
buffer cap on an adversarial infinite stream, is best-effort.

## Interactive guardrails

Opt-in per-command gating of an interactive shell. Each typed command line is run
through the same risk+policy assessment as `command_exec`:

```yaml
ssh_gateway:
  guardrail:
    mode: enforce        # off (default) | audit | enforce
```

- **off** — no interception (zero overhead).
- **audit** — the command runs; the verdict is recorded (`interactive_command`).
- **enforce** — a denied command is discarded before it runs (its Enter is
  replaced with a kill-line) and the client sees a policy notice.

**Best-effort by design:** a PTY does its own line editing (readline history,
arrow/escape sequences, bracketed paste), so command reconstruction can desync.
Enforce is a speed-bump layered on the **authoritative** target-side
command-risk gate that fires when the command actually executes — not a security
boundary. Use it for guidance/audit, not as your only control.

## Bastion / ProxyJump

The gateway is a normal SSH server, so it sits behind an existing bastion with
standard client config — no honey-specific setup:

```
# ~/.ssh/config
Host honey-gw
  HostName gateway-host
  Port 12222
  ProxyJump bastion.example.com
  IdentityFile ~/.ssh/alice
  CertificateFile ~/.ssh/alice-cert.pub
```

Then `ssh -t honey-gw <resource>`. To restrict the bastion to only forward to the
gateway, use `PermitOpen gateway-host:12222` on the bastion.

## Configuration reference

```yaml
ssh_gateway:
  listen: "0.0.0.0:12222"     # default localhost:12222
  host_key: ""                # dir for the host key (default: state dir)
  trusted_ca:                 # CA public key files (else the built-in ssh-ca)
    - /etc/honey/ssh_ca.pub
  user_attr: principal        # audit label
  cert_attr: principal        # actor field: principal | key_id
  record: true                # write .hrec.jsonl recordings
  policy_dir: /etc/honey/policy
  mask:
    values: []
    patterns: []
  guardrail:
    mode: off                 # off | audit | enforce
```

Flags on `honey ssh-server` (`--listen`, `--host-key`, `--trusted-ca`,
`--user-attr`, `--cert-attr`, `--no-auth`) override the config block.

## Security model

- **Bounded action space** — a user can only reach hosts in the inventory and can
  only do what the gates allow; the gateway holds the target credentials.
- **Deny-by-default** — no trusted CA ⇒ the gateway will not start (except
  `--no-auth` for dev). Certificate signature, type, expiry, and principal are all
  enforced by `ssh.CertChecker`.
- **Authoritative gates** — `interactive_session` / `command_exec` / `tunnel` OPA
  actions plus the deterministic command-risk floor apply to every session; these
  are the real controls.
- **Best-effort layers** — output masking (pattern/value based) and interactive
  guardrails (PTY line reconstruction) are defense-in-depth with the documented
  limits above, not substitutes for policy.
