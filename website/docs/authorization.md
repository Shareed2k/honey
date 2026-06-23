---
id: authorization
title: Authorization (OPA)
slug: /authorization
---

Honey can enforce **policy-as-code** authorization with [Open Policy Agent
(OPA)](https://www.openpolicyagent.org/). Instead of a single shared token that
grants all-or-nothing access, you write [Rego](https://www.openpolicyagent.org/docs/latest/policy-language/)
policies that decide — per actor, per host, per command — what may run.

OPA is **opt-in**: with no policy configured, Honey behaves exactly as before.
The engine runs **embedded** (in-process), so there is no sidecar to deploy.

## Enabling

Point Honey at a directory of `.rego` files (package `honey`) when starting the
web server:

```bash
export HONEY_POLICY_DIR=/etc/honey/policies
honey web
```

Every policy lives in `package honey` and sets `allow` (and optionally
`deny_reason`, `decision`, `requires`). A minimal allow-all default ships
embedded; your directory overrides it.

```rego
package honey
import rego.v1

default allow := false
default deny_reason := ""

# allow read-only API traffic
allow if input.action == "api_request"
```

## Actor identity

Decisions need to know *who* is acting. Honey resolves the actor for each
authenticated request in this order:

1. **Trusted-proxy header** — `X-Honey-User`, honored only when the request's
   peer is in `HONEY_TRUSTED_PROXIES` (CSV of CIDRs / IPs). This is the Grafana
   `X-WEBAUTH-USER` pattern: an auth proxy (Caddy, oauth2-proxy, Authelia)
   authenticates the user and injects the header.
2. **JWT** — an Ed25519-signed bearer token whose `sub` claim is the actor.
   Enable with `HONEY_JWT_PUBLIC_KEY` (base64 Ed25519 public key).
3. **Fallback** — `api` (the shared-token caller, which carries no identity).

```bash
export HONEY_TRUSTED_PROXIES="10.0.0.0/8,192.168.0.0/16"
export HONEY_JWT_PUBLIC_KEY="$(base64 < ed25519_pub.raw)"
```

Webhook runs resolve their actor from a trusted header/JWT, else the recipe's
`webhook.actor` (a gjson path into the payload, e.g. `sender.login`), else
`webhook:<app>`.

## Decision points

A single enforcer is consulted at several `action`s. Your policy branches on
`input.action`:

| `input.action` | When | Key input fields |
| --- | --- | --- |
| `api_request` | Every authenticated `/api/v1/*` request | `actor`, `method`, `path` |
| `recipe_execute` | Before a recipe runs (admission) | `actor`, `recipe`, `hosts`, `execution` |
| `step_execute` | Per host, before a step's command | `actor`, `step_kind`, `host`, `host_meta`, `host_vars` |
| `command_exec` | Per command/script, with risk signals | `actor`, `command`, `target`, `execution` (see [Command Risk](/command-risk)) |
| `interactive_session` | Before opening a web SSH shell | `actor`, `target` |
| `recipe_approve` | When approving a held run | `approver`, `requester`, `recipe` |

`step_execute` filters the host list: denied hosts are skipped (and shown as
skipped in the run output) while allowed hosts proceed.

## Inventory as policy data

Your config [inventory](/inventory) is exposed to policies as the OPA **data**
document `data.inventory` (global `vars`, CEL `groups`, per-host `hosts`).
Additionally, each host's **resolved** inventory variables are passed as
`input.target.host_vars` at `step_execute` / `command_exec`, so a policy can
gate on a host's effective tier:

```rego
allow := false if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.host_vars.tier == "prod"
}
```

## Approval flow

A policy may return `decision := "require_approval"` instead of a hard deny.
The run is then **held**: the API responds `202 {status:"pending_approval", id}`
rather than executing.

```rego
decision := "require_approval" if {
	input.action == "recipe_execute"
	not input.execution.approved
}
allow if { input.action == "recipe_execute"; input.execution.approved }
```

An authorized actor approves it:

```bash
curl -XPOST $HONEY/api/v1/approvals/$ID -d '{"decision":"approve"}'
```

The approval is itself gated by `recipe_approve` (e.g. require the approver to
differ from the requester):

```rego
allow if {
	input.action == "recipe_approve"
	input.approver != input.requester
}
```

Re-submitting the run with the `approval_id` then proceeds. Pending runs live in
an in-memory store with a 24h TTL; list them with `GET /api/v1/approvals`.

## Biometric step-up (WebAuthn)

For the highest-risk operations a policy can demand a fresh passkey assertion:

```rego
decision := "require_biometric" if {
	input.action == "recipe_execute"
	input.target.env == "prod"
}
allow if { input.action == "recipe_execute"; input.execution.biometricVerified }
```

Enable WebAuthn and the `/api/v1/webauthn/*` register/assert endpoints:

```bash
export HONEY_WEBAUTHN_RPID=honey.example.com
export HONEY_WEBAUTHN_ORIGIN=https://honey.example.com
export HONEY_WEBAUTHN_SECRET=<random-32-bytes>   # signs biometric tokens
```

After a successful assertion the client receives a short-lived
`biometric_token`, replayed as the `X-Honey-Biometric` header on the
`require_biometric` run.

## In-recipe policy checks (`opa` step)

Recipes can evaluate a policy inline and fail/branch on the result:

```cue
steps: [
	{
		host: "_"
		opa: { policy: "policies/compliance.rego" }
	},
]
```

The step compiles the referenced `.rego` (relative to the recipe dir),
evaluates it with `{actor, recipe, ...custom input}`, and fails the step when
`allow == false` — usable with `when` / `depends` to gate later steps.

## Environment variables

| Variable | Effect |
| --- | --- |
| `HONEY_POLICY_DIR` | Directory of `.rego` files; enables all OPA gates |
| `HONEY_JWT_PUBLIC_KEY` | base64 Ed25519 public key; enables JWT identity |
| `HONEY_TRUSTED_PROXIES` | CSV of CIDRs/IPs trusted to assert `X-Honey-User` |
| `HONEY_WEBAUTHN_RPID` / `_ORIGIN` / `_SECRET` | enable passkey biometric step-up |

See [Command Risk Engine](/command-risk) for the deterministic command analysis
that feeds the `command_exec` decision.
