---
id: jit-access
title: JIT Access & Share Links
slug: /jit-access
---

From the [web UI](./web-ui.md)'s search results, any connectable record can be
turned into a **time-boxed share link**: a QR code + URL that hands a
recipient either a **browser terminal** or a **short-lived SSH certificate**,
without giving them a honey login. Each link is either a direct grant
("Grant now", immediately active) or a request that stays **Pending** until an
approver decides it. This is a purely web-driven feature — there is no
`honey jit` CLI (yet); grants are created, listed, and decided from the web UI
or its API.

## Quick start

1. In the search results, open a record and click **Share**.
2. Pick a duration, capabilities, and delivery mode, then **Create link**.
3. Copy the link (`https://<web-host>/?access=<code>`) — it's shown once — and
   send it to the recipient.
4. The recipient opens the link and sees **Open terminal in browser** and/or
   **Get certificate**, depending on the delivery mode.

## Delivery modes

Set on creation (`delivery` field):

| Mode | What the recipient gets |
| --- | --- |
| `web` | A browser terminal (WebSocket), same pipeline the [web UI's terminal](./web-ui.md#browser-terminal) uses. |
| `cert` | A short-lived SSH user certificate they can use with a native `ssh` client. |
| `both` | Either, recipient's choice — the status page offers both. |

## Capabilities

`capabilities` is a subset of `shell`, `exec`, `tunnel`. They gate what the
redeem endpoints will honor:

- The **`web`** (browser terminal) redeem requires `shell`.
- The **`cert`** redeem requires `shell`, `exec`, or `tunnel` (a certificate is
  a general-purpose credential; what it can actually do on the target is still
  bounded by the target-side gates).
- `tunnel` is delivered as a **certificate** — the recipient uses it for
  `ssh -L` port-forwarding through the gateway (the signed certificate carries
  `permit-port-forwarding`, and each forward is authorized by the OPA `tunnel`
  action). A tunnel grant therefore needs `cert` (or `both`) delivery; a
  `web`-only tunnel link has no redeemable action, and the Share dialog blocks
  that combination.

## Approval + notification

`require_approval: true` creates the grant as **Pending** instead of
**Approved**; its share link exists but every redeem endpoint reports it
inactive until an approver decides it. Decide it from the **Access Requests**
tab in the web UI (approve / deny a pending request, or revoke an active one),
or over the API:

```
POST /api/v1/jit/grants/{id}
{"decision": "approve"}   // or "deny", or "revoke" on an active grant
```

Approving is itself OPA-gated (`jit_approve`) and the API rejects an approver
deciding their own request when a policy enforces that. While a link is
Pending, the recipient's redeem page polls and flips to the live
terminal/certificate offers on its own once it is approved — no reload.

On creation of a Pending grant, honey sends a **best-effort notification**
through whatever [recipe notify](./cue-recipes.md) backend is configured via
environment variables — `HONEY_NOTIFY_SLACK_WEBHOOK_URL`,
`HONEY_NOTIFY_HTTP_URL`, `HONEY_NOTIFY_TELEGRAM_BOT_TOKEN` /
`HONEY_NOTIFY_TELEGRAM_CHAT_IDS`. The message names the requester, resource,
capabilities, and the grant id to approve — **it never contains the redeem
code**. With no notify backend configured, nothing is sent and the grant just
sits Pending until someone reviews the **Access Requests** tab (or
`GET /api/v1/jit/grants`).

## Endpoints

Authenticated (session token / normal web auth), used by the operator side:

| Method & path | Purpose |
| --- | --- |
| `POST /api/v1/jit/grants` | Create a grant. Body: `resource`, `capabilities`, `delivery`, `duration`, `reason`, `require_approval`, `max_redemptions`, `recipient`. Returns the plaintext `code` **once**. |
| `GET /api/v1/jit/grants` | List all grants (redacted — never the code or its hash). |
| `POST /api/v1/jit/grants/{id}` | Decide a grant: `{"decision": "approve" \| "deny" \| "revoke"}`. |

Code-authenticated (no session — the link's code *is* the credential), and
mounted **outside** the normal auth group:

| Method & path | Purpose |
| --- | --- |
| `GET /api/v1/jit/redeem/{code}` | Status/lobby view: what the link offers, without consuming a redemption. |
| `POST /api/v1/jit/redeem/{code}/cert` | Consume a redemption, mint an SSH certificate for the caller's supplied public key. |
| `GET /api/v1/jit/redeem/{code}/terminal` | WebSocket: consume a redemption, open a live browser terminal to the granted resource. |

## Policy gates

- **`jit_grant`** — gates creating a grant (`{actor, target, capabilities, delivery, require_approval}`), evaluated on `POST /jit/grants`.
- **`jit_approve`** — gates deciding a Pending grant (`{actor, approver, requester, target}`), evaluated on approve.
- **`interactive_session`** — the same gate the [web UI](./web-ui.md) and [SSH gateway](./ssh-gateway.md) use for any interactive shell, re-evaluated on the browser-terminal redeem (actor is the grant's recipient, or `share:<id>` if none was set).

These OPA gates, together with the SSH CA's certificate validity window, are
the **authoritative** controls — everything else (expiry, redemption caps,
generic 404s) is defense-in-depth around them. A nil policy enforcer allows by
default, same as elsewhere in honey.

## Audit

Every grant lifecycle event is written to the [audit log](./authorization.md)
with `source=web`, visible via `honey audit tail` / `honey audit export`:

- `jit_created` — a grant was created (`decision=allow` for a direct grant, `require_approval` for one needing approval).
- `jit_decided` — an approver approved or denied a Pending grant.
- `jit_revoked` — an active or pending grant was revoked.
- `jit_redeemed` — a redeem endpoint successfully consumed a redemption (`extra.delivery` is `web` or `cert`).

## Security model and limits

Be aware of what this feature does and does not protect against:

- **The code is the credential.** It appears directly in the URL
  (`?access=<code>`) — treat a share link exactly like a password or an API
  key. Anyone with the link can redeem it (subject to the gates above) until
  it expires or is revoked.
- Codes are 32 random bytes; only their SHA-256 hash is ever persisted, and
  hash comparisons use a constant-time compare. The plaintext code is
  returned exactly once, from the create response.
- Every redeem failure — unknown code, expired, revoked, denied, wrong
  delivery mode, wrong capability, redemption cap hit — collapses to the same
  generic 404. A recipient (or attacker) cannot distinguish "wrong code" from
  "right code, wrong state."
- Redemptions are bounded by `max_redemptions` (0 = unlimited within the
  window) and the grant's time window still auto-expires regardless.
- A minted certificate's TTL is clamped to whichever is smaller: the grant's
  remaining time window, or the configured `max_duration` cap.
- The browser-terminal redeem records the session and OPA-gates it exactly
  like the rest of honey's web terminal, but — unlike the
  [SSH gateway](./ssh-gateway.md#data-masking) — it does **not** mask secrets
  out of the live output or the recording.
- Web-terminal share links currently cover SSH, Docker, Kubernetes, and
  mesh-routed records. Proxmox-serial and TrueNAS-console records are not
  supported over a share link (those are console-only targets handled by a
  separate part of the web-terminal dispatch).
- A live-terminal share's **collaborate** guest has every keystroke gated by
  the [web terminal's interactive guardrail](./web-ui.md#interactive-guardrails)
  in `enforce` mode, always — regardless of the operator's own
  `web.guard_mode` setting, since an untrusted recipient never gets a weaker
  mode via config. A **watch** guest has no input at all (read-only), so
  there is nothing to gate. Like the SSH gateway's guardrail, this is a
  best-effort speed-bump (PTY line reconstruction can desync on readline
  history, pasted input, or a shell reached through an editor/REPL) — never a
  security boundary. It calls the same OPA `command_exec` decision as every
  other command path, so **with no OPA policy configured it blocks nothing**:
  for an untrusted collaborate guest, the actual control is sharing **watch**
  (read-only) access, not this guard.
- There is no `honey jit` CLI. Manage grants from the web UI's Share button or
  directly against the API.

## Configuration reference

An absent `jit:` block means JIT is enabled with built-in defaults (1h
default duration, 24h max duration, store under the state dir).

```yaml
jit:
  enabled: true                          # false disables the feature entirely (endpoints report 503)
  store_path: ""                         # grant store path (default: <state dir>/jit_grants.jsonl)
  default_duration: "1h"                 # used when a create request omits duration
  max_duration: "24h"                    # hard cap on any grant's access window and cert TTL
```

`default_duration` and `max_duration` accept any Go duration string (e.g.
`30m`, `2h`); an unparseable or non-positive value is ignored and the
built-in default is kept. Setting `enabled: false` stops honey from
constructing a grant store at all — `POST /api/v1/jit/grants` and the redeem
endpoints then report `503 Service Unavailable`.

Notification backends are configured the same way as
[recipe notifications](./cue-recipes.md), via environment variables:

```bash
export HONEY_NOTIFY_SLACK_WEBHOOK_URL=https://hooks.example/services/...
export HONEY_NOTIFY_HTTP_URL=https://example.internal/hooks/jit
export HONEY_NOTIFY_TELEGRAM_BOT_TOKEN=...
export HONEY_NOTIFY_TELEGRAM_CHAT_IDS=123456789
```

Any subset may be set; with none set, Pending grants simply are not
announced and rely on someone reviewing the **Access Requests** tab (or
`GET /api/v1/jit/grants`).
