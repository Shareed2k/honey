---
id: intercept
title: Local Interception
slug: /intercept
---

`honey intercept <pod> [-- <cmd>]` runs a local process **as if it were
running inside a Kubernetes pod**: its outbound TCP/UDP connections and DNS
resolution leave from the pod's network (so a cluster Service name resolves
and connects exactly as it would from inside the pod), inbound traffic
addressed to the pod can be stolen or mirrored to the local process instead,
and local file reads can resolve against the pod's (sidecar-visible)
filesystem root. This lets you run and debug a service on your own machine —
your editor, your debugger, your breakpoints — while it behaves, on the wire,
like a workload in the cluster.

honey is the **authorizing, auditing control plane**: every session is
[OPA-gated](#the-intercept-opa-gate) and [audited](#audit) before anything is
touched. The actual interception — redirecting traffic, terminating tunnels,
serving files — is a separate **data-plane agent** that honey deploys into
the target pod as a Kubernetes **ephemeral container**; honey never proxies
the intercepted traffic itself.

This is opt-in: with no `intercept:` block configured, `honey intercept`
reports "not configured" and nothing else changes.

## Quick start

```yaml
# config.yaml
intercept:
  enabled: true
  agent_image: registry.example.com/honey-intercept-agent:latest
  default_mode: ["egress", "files"]
```

```bash
# Run `curl` locally as if it were the "web" container of pod "api-7d9f":
# DNS and TCP/UDP egress leave from the pod's network namespace.
honey intercept api-7d9f -n prod --container web --mode egress -- \
  curl http://payments.prod.svc.cluster.local:8080/healthz
```

## Config

The `intercept:` block enables the command and supplies its operator-level
defaults:

```yaml
intercept:
  enabled: true                                              # required to enable `honey intercept`
  agent_image: registry.example.com/honey-intercept-agent:v1  # the data-plane agent image deployed as an ephemeral container
  default_mode: ["egress", "files"]                          # modes used when --mode is omitted (egress|incoming|files)
  policy_dir: /etc/honey/intercept-policy                    # OPA policy directory for the intercept gate (optional)
```

- **`enabled`** — must be `true` for `honey intercept` to run at all. Absent
  or `false` ⇒ the command reports "not configured".
- **`agent_image`** — the container image for the data-plane agent honey adds
  to the target pod as an ephemeral container. This is an operator-configured
  value; honey does not ship or pin a specific image.
- **`default_mode`** — the modes (`egress`, `incoming`, `files`) used when
  `--mode` is not passed on the command line.
- **`policy_dir`** — an OPA policy directory dedicated to the `intercept`
  action. If set, it takes precedence for this command; if empty, `honey
  intercept` falls back to the same policy resolution as every other honey
  gate (`HONEY_POLICY_DIR` / `defaults.policy_dir`) — see
  [Authorization](./authorization.md).

## The `intercept` OPA gate

Every session is authorized before anything is deployed. honey evaluates:

```json
{
  "action": "intercept",
  "actor": "alice@corp.example",
  "cluster": "prod",
  "namespace": "payments",
  "pod": "api-7d9f",
  "container": "web",
  "mode": ["egress", "files"],
  "agent_image": "registry.example.com/honey-intercept-agent:v1"
}
```

This is **fail-closed**: an evaluation error and an explicit non-allow both
deny the session — nothing defaults to allow inside this gate. Like every
other honey policy, the *embedded* default policy honey ships is permissive
(`allow := true`) so OPA remains opt-in; to actually restrict who can
intercept what, write an explicit policy for the `intercept` action, for
example allowing only a named team into a `staging` namespace and denying
everything else:

```rego
package honey
import rego.v1

default allow := false

# platform-team may intercept any pod in the "staging" namespace
allow if {
	input.action == "intercept"
	input.actor in {"alice@corp.example", "bob@corp.example"}
	input.namespace == "staging"
}
```

No `allow` for `action == "intercept"` ⇒ no ephemeral container is deployed
and no session starts.

### Audit

honey records one `intercept_start` event when a session begins and one
`intercept_stop` event when it ends (with the elapsed duration and a stop
reason such as `completed` or `canceled`). Both carry the actor, cluster,
namespace, pod, container, mode, and agent image — **never** the session
token or any intercepted payload:

```bash
honey audit tail --action intercept_start
honey audit tail --action intercept_stop
```

A gate denial is never audited as a start — since nothing was deployed, there
is no session to record.

## Usage

```bash
honey intercept <pod> [flags] [-- <command> [args...]]
```

| Flag | Description |
| --- | --- |
| `--namespace`, `-n` | Namespace of the target pod. |
| `--container` | Target container the agent shares namespaces with. |
| `--mode` | Interception mode; repeatable: `egress`, `incoming`, `files`. Defaults to `intercept.default_mode`. |
| `--target` | Local address that incoming traffic is delivered to. Required when `--mode incoming` is set. |
| `--udp` | Also tunnel UDP traffic (egress and/or incoming) alongside TCP. |
| `--cluster` | Target cluster name (gating, audit, and port-forwarding). |
| `--agent-image` | Overrides `intercept.agent_image` for this session. |

The interception stays up for as long as `<command>` runs; without a
trailing `-- <command>`, the session runs until interrupted (Ctrl-C), which
cancels and tears the session down.

### Egress (TCP/UDP + DNS)

Outbound connections and DNS lookups from `<command>` leave through the
pod's network — a cluster-internal Service name resolves and connects just
as it would from inside the pod:

```bash
honey intercept api-7d9f -n prod --container web --mode egress -- \
  curl http://payments.prod.svc.cluster.local:8080/healthz
```

Add `--udp` to also carry UDP egress (for example a UDP health check or a
DNS query issued directly rather than through the standard resolver):

```bash
honey intercept api-7d9f -n prod --container web --mode egress --udp -- \
  my-udp-client payments.prod.svc.cluster.local:5353
```

### Incoming (steal or mirror)

`incoming` mode requires `--target`, the local address inbound pod traffic is
delivered to:

```bash
honey intercept api-7d9f -n prod --container web \
  --mode incoming --target 127.0.0.1:8080 -- \
  my-local-server --port 8080
```

Add `--udp` to also steal/mirror UDP traffic to the same target.

### Files

`files` mode resolves `<command>`'s file reads against the pod's
(sidecar-visible) filesystem root, so it can read configuration or data files
that only exist inside the pod:

```bash
honey intercept api-7d9f -n prod --container web --mode files -- \
  cat /etc/app/config.yaml
```

Modes combine freely — `--mode egress,files` runs a command whose network
egress leaves from the pod and whose file reads resolve against the pod's
root in the same session.

## Prerequisites & limits

- **Ephemeral containers** must be available on the target cluster (GA since
  Kubernetes 1.25). Older clusters, or clusters that disable the feature,
  cannot run `honey intercept`.
- **`NET_ADMIN` + the pod's network namespace.** The deployed agent runs with
  the `NET_ADMIN` capability and targets the chosen container's namespaces
  (`TargetContainerName`) so it can install the redirects and tunnels
  interception needs. Some hardened clusters (Pod Security admission,
  restricted PSPs/PSAs, or an admission webhook) block `NET_ADMIN` or
  ephemeral containers outright — check your cluster's pod security policy
  before relying on this.
- **RBAC.** The identity honey uses to reach the cluster needs, at minimum:

  ```yaml
  apiVersion: rbac.authorization.k8s.io/v1
  kind: ClusterRole
  metadata:
    name: honey-intercept
  rules:
    - apiGroups: [""]
      resources: ["pods"]
      verbs: ["get"]
    - apiGroups: [""]
      resources: ["pods/ephemeralcontainers"]
      verbs: ["get", "patch", "update"]
    - apiGroups: [""]
      resources: ["pods/exec"]
      verbs: ["create"]
    - apiGroups: [""]
      resources: ["pods/portforward"]
      verbs: ["create"]
  ```

- **The ephemeral container cannot be removed.** Kubernetes has no API to
  delete an ephemeral container once added — it is a permanent (if inert)
  entry in the pod spec after the session ends. The agent **self-terminates**
  at the end of a session; it does not keep running or keep `NET_ADMIN`
  redirects installed, but its container entry remains visible in
  `kubectl describe pod`.
- **macOS injection is limited by System Integrity Protection (SIP).** SIP
  blocks the local injection mechanism from attaching to Apple-signed system
  binaries; injecting into your own build or a non-system binary is
  unaffected.
- **Sessions are short-lived and always gated.** Every session re-runs the
  [`intercept` gate](#the-intercept-opa-gate) and is fully torn down (agent
  signaled to exit, port-forwards closed, the local session token and
  extracted injector removed) when `<command>` exits or the session is
  interrupted — there is no persistent or background interception left
  running after `honey intercept` returns.
