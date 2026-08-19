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
  agent_image: ghcr.io/shareed2k/mogate:0.1.3   # stock agent image (0.1.3+ waits for its token file)
  default_mode: ["egress", "files"]
```

honey deploys the **stock** agent image as-is and delivers the per-session
token to it after the container starts; the agent waits for that token
(`v0.1.2+`), so there is no wrapper to build and nothing to pre-install in the
pod.

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
  agent_image: ghcr.io/shareed2k/mogate:0.1.3               # the stock data-plane agent image (waits for its token file)
  default_mode: ["egress", "files"]                          # modes used when --mode is omitted (egress|incoming|files|env)
  policy_dir: /etc/honey/intercept-policy                    # OPA policy directory for the intercept gate (optional)
```

- **`enabled`** — must be `true` for `honey intercept` to run at all. Absent
  or `false` ⇒ the command reports "not configured".
- **`agent_image`** — the container image for the data-plane agent honey adds
  to the target pod as an ephemeral container. The **stock** `mogate` image
  works as-is: honey delivers the session token by writing it into the running
  container (over the API server, out of argv and the environment), and the
  agent waits for that token file at startup — so the image needs no wrapper
  and nothing pre-installed. Any image whose agent waits for `--token-file`
  works; pin an immutable tag for production.
- **`default_mode`** — the modes (`egress`, `incoming`, `files`, `env`) used
  when `--mode` is not passed on the command line. (`env` is targeted-only, so a
  default that includes it still requires naming a target pod — see
  [Environment variables](#environment-variables).)
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

`<pod>` is optional: omitting it entirely runs a [targetless](#targetless-no-target-pod)
(egress-only) session against honey's own standalone agent Pod instead of an
existing workload's pod.

| Flag | Description |
| --- | --- |
| `--namespace`, `-n` | Namespace of the target pod. Defaults to the kubeconfig context's namespace (falling back to `default`), exactly like `kubectl`. |
| `--container` | Target container the agent shares namespaces with. |
| `--mode` | Interception mode; repeatable: `egress`, `incoming`, `files`, `env`. `env` is targeted-only. Defaults to `intercept.default_mode`. |
| `--target` | Local address that incoming traffic is delivered to. Required when `--mode incoming` is set. |
| `--env-include` | With `--mode env`, overlay **only** these target env var names (repeatable, comma-separated; mutually exclusive with `--env-exclude`). |
| `--env-exclude` | With `--mode env`, drop these target env var names from the overlay, on top of the built-in denylist (repeatable, comma-separated; mutually exclusive with `--env-include`). |
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

### Environment variables

`env` mode overlays the **target container's environment** onto `<command>`,
so a service you run locally sees the same `DATABASE_URL`, feature flags, and
other configuration the pod's own process would:

```bash
honey intercept api-7d9f -n prod --container web --mode egress,env -- ./my-service
# ./my-service now reads the pod's DATABASE_URL, FEATURE_FLAGS, etc.,
# while its own PATH/HOME and language-toolchain vars stay local.
```

**Where the values come from.** The agent reads the target container's
`/proc/1/environ` (PID 1 of the shared PID namespace) and hands it back over
the relay. honey fetches it **once, just before** `<command>` starts, and
overlays it onto the command's own environment. For a name present both
locally and in the target, the **target value wins**; a variable that exists
only locally is **kept**; honey's own injector/loader variables always win
over a same-named target value.

**What is always kept local.** honey never overlays the variables that select
executables, interpreters, or language-toolchain paths — importing the pod's
values would make your local command resolve the wrong binaries. This built-in
denylist is fixed and cannot be overlaid:

`PATH`, `HOME`, `HOMEPATH`, `CLASSPATH`, `JAVA_EXE`, `JAVA_HOME`,
`JAVA_TOOL_OPTIONS`, `_JAVA_OPTIONS`, `CATALINA_HOME`, `GEM_HOME`, `GEM_PATH`,
`GOPATH`, `PYTHONPATH`, `BUNDLE_PATH`, `BUNDLE_BIN_PATH`, `BUNDLE_GEM_PATH`,
and any `BUNDLER_ORIG_*` variable.

**Narrowing the overlay.** Two mutually exclusive filters refine which names
are overlaid, on top of that denylist:

```bash
# overlay ONLY these names (an allow-list)
honey intercept api-7d9f -n prod --mode env \
  --env-include DATABASE_URL,REDIS_URL -- ./my-service

# overlay everything EXCEPT these names (extends the built-in denylist)
honey intercept api-7d9f -n prod --mode env \
  --env-exclude OTEL_EXPORTER_OTLP_ENDPOINT -- ./my-service
```

Both flags are repeatable and comma-separated, and they carry **names only** —
never values. Values are never logged, gated, or audited: honey fetches them
solely to hand to your command.

**Targeted-only.** `env` needs a target container to read from, so it is
rejected on a [targetless](#targetless-no-target-pod) session (there is no
target pod). `honey intercept --mode env` with no `<pod>` fails with a clear
error; name a pod.

**Extra privilege, added only for `env`.** Reading another process's
`/proc/1/environ` is a `PTRACE_MODE_READ_FSCREDS` operation, so for a non-root
target the agent needs `CAP_SYS_PTRACE` and `CAP_DAC_READ_SEARCH`. honey adds
those two capabilities to the ephemeral container **only when `env` is active**
(least privilege — the network-only modes keep their `NET_ADMIN`-only context).
A `restricted`-PSA namespace may refuse them the same way it already refuses
the targeted agent's root + `NET_ADMIN` (see [Prerequisites &
limits](#prerequisites--limits)); the overlay is **best-effort**, so if the
agent cannot read the environ, honey runs `<command>` with its local
environment unchanged rather than failing the session.

**Agent image requirement.** `env` mode needs an agent that answers the
environment request, which is `mogate` **`v0.1.9`+**
(`ghcr.io/shareed2k/mogate:0.1.10`). Older agents predate it and cannot serve
`env` mode.

## Targetless (no target pod)

`honey intercept` also runs with **no `<pod>` argument at all**:

```bash
honey intercept -n apps -- curl http://payments.apps.svc.cluster.local:8080/healthz
```

Omitting `<pod>` puts the session in **targetless** mode. Instead of adding an
ephemeral container to an existing workload's pod (everything documented
above), honey deploys **its own standalone agent Pod** into the resolved
namespace and gives the local `<command>` **egress and DNS through the
cluster** — the same wire behavior as `--mode egress` in the targeted path.
There is no incoming steal/mirror and no file redirection in targetless mode
(it is egress-only), and `--container`/`--target` have no meaning without a
target pod — naming a pod is required for those.

**Why targetless exists**: the targeted (ephemeral-container) path shares the
network namespace of a real workload pod, so the session dies the instant
that pod does — a rollout, a redeploy, anything that replaces the pod out
from under it. The standalone agent Pod is not tied to any workload's
lifecycle, so a long-running local task (a batch job, a soak test) keeps its
egress path alive even while the target application is redeployed
underneath it.

**Minimal privilege**: the standalone agent Pod runs **non-root**, with
`NET_ADMIN` — and every other Linux capability — dropped. Because targetless
has no target namespace to redirect, egress and DNS are handled entirely in
userspace, so the agent needs no elevated privilege to do its job. This is
the opposite of the targeted path's ephemeral container, which must run as
root with `NET_ADMIN` to program the target's nftables (see [Prerequisites &
limits](#prerequisites--limits)) — and it means targetless mode also works in
`restricted`-PSA namespaces where the targeted agent's root+NET_ADMIN
requirement is refused outright.

**`agent_image` requirement**: the configured `intercept.agent_image` (or
`--agent-image`) must be a mogate build whose agent supports `--no-redirect`
— the egress-only flag honey passes when it runs the standalone Pod. This is
a newer capability than the plain token-file-wait support the targeted path
needs (see [Config](#config)); an agent image built before `--no-redirect`
existed will fail to start under targetless mode.

**Lifecycle**: the standalone Pod is created in the resolved namespace
(`--namespace`/`-n`, or the kubeconfig context's default namespace — the same
resolution the targeted path uses) under a fresh, unique name. It is labeled
`app.kubernetes.io/managed-by=honey-intercept` so an operator, or a cleanup
job, can find and remove any Pod left behind if honey itself crashes before
teardown runs. On a normal exit honey deletes the Pod outright — unlike the
targeted path's ephemeral container, which Kubernetes has no API to remove at
all (see [Prerequisites & limits](#prerequisites--limits)), the standalone
Pod is entirely honey's own to create and destroy.

| | Targeted (`honey intercept <pod>`) | Targetless (`honey intercept`) |
| --- | --- | --- |
| Agent runs as | an ephemeral container in the target pod | its own standalone Pod |
| Privilege | root + `NET_ADMIN` (installs nftables) | non-root, every capability dropped |
| Modes | egress, incoming, files | egress only |
| Lifecycle | shares the target pod's — dies with it; the ephemeral container can never be removed afterward | independent of any workload; honey deletes the Pod on teardown |
| Works in `restricted`-PSA namespaces | no (needs root + `NET_ADMIN`) | yes |
| Survives the target workload being redeployed | no | yes |

## Server-brokered (SSO) interception

Everything above is the **direct** path: the CLI builds its Kubernetes client
from your own kubeconfig and runs the whole session itself — gate, deploy,
token delivery, port-forward. That makes the [intercept gate](#the-intercept-opa-gate)
**advisory**: it runs in your own process, against your own cluster
credentials, so it only stops you if you choose to go through `honey
intercept` in the first place. A user who already holds credentials able to
create an ephemeral container can deploy one directly and skip the gate
entirely.

When `honey web` is configured with SSO login and a cluster registry, `honey
intercept <pod>` instead uses the **server-brokered** path, and picks it up
automatically: if `--admin-url` (or `$HONEY_WEB_URL`) points at a honey web
that reports brokered intercept enabled, the CLI runs a browser SSO sign-in,
and honey web itself — not the CLI — verifies your identity, evaluates the
gate, and deploys the agent using **honey's own cluster service-account**.
Your own cluster credentials are only ever used to port-forward to the agent
honey already deployed; they are never used to create one. This makes the
gate **authoritative**: there is no code path, other than honey's own gated
`authorize` endpoint, that can put an interception agent into a pod.

Those port-forward credentials come from `honey kube login <cluster>`: it
writes a `honey-<cluster>` kubeconfig context pointing at the honey access
proxy, and `honey intercept --cluster <cluster>` **picks that context up
automatically** — you don't need a separate local cluster mapping. Port-forward
traffic then flows through the honey proxy under your impersonated identity,
which is exactly where the [RBAC split](#the-rbac-split) is enforced (your
identity has `pods/portforward` but not `pods/ephemeralcontainers`). The
typical operator flow is therefore two commands:

```bash
honey kube login prod                         # once: SSO → honey-prod kubeconfig context
honey intercept api-7d9f -n prod --cluster prod --mode egress -- curl ...
```

If you would rather point at a specific kubeconfig, an explicit
`k8s_proxy.clusters` entry naming the cluster in your **own** config overrides
the login context. honey never silently falls back to your current kubeconfig
context — the port-forward must reach the same cluster honey authorized and
audited, so an unknown `--cluster` is an error, not a guess.

```
honey intercept <pod>  (brokered)
      │ 1. browser SSO sign-in                     (same flow as `honey kube login`)
      ▼
honey web  POST /api/v1/intercept/authorize
      │ 2. verify id_token, resolve identity, evaluate the intercept gate
      │ 3. deploy the agent as an ephemeral container     — honey's cluster SA
      │ 4. deliver the per-session token to the agent     — honey's cluster SA
      ▼
   {session_id, token, control_port, egress_port, expires_at}
      │
      ▼
CLI  5. port-forward to the agent's ports                 — YOUR cluster identity
     6. run <command> against those ports with the returned token
      │ (command exits, or Ctrl-C)
      ▼
honey web  POST /api/v1/intercept/{session_id}/stop        — signals + tears down
```

If no honey web / SSO is reachable, `honey intercept` falls back to the
direct path unchanged — the direct path is not being removed, only
documented as the weaker of the two.

### The RBAC split

The split between the identity that *deploys* the agent and the identity
that *connects* to it is the entire authoritative boundary, and it is
enforced by your cluster's RBAC, not by honey:

| Principal | `pods` | `pods/ephemeralcontainers` | `pods/exec` | `pods/portforward` |
| --- | --- | --- | --- | --- |
| **honey web's cluster service-account** (deploys) | get | get, patch, update | create | — |
| **the operator's own cluster identity** (connects) | get | **none** | — | create |

honey cannot enforce this split — it is a **cluster-side RBAC prerequisite**
you must configure, exactly like the [Kubernetes access
proxy's `impersonate` grant](./k8s-proxy.md#cluster-side-rbac-prerequisite).
If the operator's own identity is also granted `pods/ephemeralcontainers`,
that operator can bypass the gate the same way the direct path always could.
The value of the split is that, done correctly, the operator's own
credentials are mechanically incapable of creating an ephemeral container —
so honey's gated `authorize` endpoint becomes the *only* way to get an agent
into a pod. Once honey has deployed one, port-forwarding to it is inert on
its own: driving the agent requires the per-session token, and honey hands
that token only to the caller who passed the gate. The token is the
capability; the gate decides who receives it.

### Claims model

On the brokered path, the [intercept gate](#the-intercept-opa-gate) receives
your full verified id_token claim set, not just a group list. `gate()` adds
`subject`, `email`, `groups`, and `claims` (the complete decoded claim map)
to the OPA input alongside the existing `action`/`actor`/`cluster`/
`namespace`/`pod`/`container`/`mode`/`agent_image` fields:

```json
{
  "action": "intercept",
  "actor": "alice@corp.example",
  "cluster": "prod",
  "namespace": "payments",
  "pod": "api-7d9f",
  "container": "web",
  "mode": ["egress", "files"],
  "agent_image": "registry.example.com/honey-intercept-agent:v1",
  "subject": "user-abc123",
  "email": "alice@corp.example",
  "groups": ["eng", "on-call"],
  "claims": { "...": "the full decoded id_token claim set" }
}
```

These fields are added only when populated, so the direct path's input — and
any existing `intercept` policy written before brokered mode existed — is
unchanged. This mirrors exactly what the [`identity`
policy](./sso-login.md#the-identity-policy) already sees at login, so a
policy author works from one familiar claim surface.

The claims are **server-side and transient**: they live only in honey web's
memory for the duration of the `authorize` request. Nothing claim-bearing is
persisted to disk or handed to the deployed agent or target pod — the agent
receives only an opaque per-session token. This is a deliberate contrast with
the [k8s access proxy](./k8s-proxy.md), whose mTLS client certificate encodes
your groups in `O=` and is written to disk in your kubeconfig; that cert path
is unrelated to (and not used by) brokered intercept.

### Config

Brokered mode activates automatically — no separate flag — when three things
are all present in the config `honey web` loads: an [`oidc:`
block](./sso-login.md#server-configuration) (SSO login), `intercept.enabled:
true`, and at least one cluster in the [`k8s_proxy.clusters`
registry](./k8s-proxy.md#configuration-reference) (honey needs a
cluster-side kubeconfig to deploy into). Missing any of the three leaves
`honey intercept` on the direct path only.

```yaml
# config.yaml
oidc:                             # same block the k8s proxy / ssh gateway use
  issuer: https://your-oidc-provider.example/realms/corp
  client_id: honey-intercept
  username_claim: email
  groups_claim: groups

intercept:
  enabled: true
  agent_image: ghcr.io/shareed2k/mogate:0.1.3
  default_mode: ["egress", "files"]
  policy_dir: /etc/honey/intercept-policy   # optional; see below
  session_ttl: 1h                           # orphan-teardown bound (default 1h)
  session_store: sqlite                     # memory (default) | sqlite | postgres
  session_store_dsn: /var/lib/honey/intercept-sessions.db  # sqlite file path or postgres DSN

k8s_proxy:
  clusters:
    - name: prod
      kubeconfig: /etc/honey/prod-honey-sa.kubeconfig   # honey's OWN service-account, granted
                                                          # per the RBAC split above — NOT your kubeconfig
```

`intercept.session_ttl`, `session_store`, and `session_store_dsn` are the
config keys that exist only for the brokered path (see [Teardown &
TTL](#teardown--ttl) and [Session store](#session-store--restart-durability)
below); everything else in the `intercept:` block is shared with the direct
path and already documented [above](#config).

### Endpoints

honey web mounts three routes, only when both `oidc:` and a working intercept
broker (`intercept.enabled` plus a cluster registry) are configured —
otherwise they don't exist (`404`), the same pattern as the SSO login
endpoints:

| Method & path | Purpose |
| --- | --- |
| `GET /api/v1/intercept/config` | Non-secret: whether brokered intercept is enabled and the configured default modes. The CLI polls this to decide direct vs. brokered. |
| `POST /api/v1/intercept/authorize` | id_token-authenticated. Verifies the token, resolves identity, evaluates the gate with claims, deploys the agent, and returns the session handle (`session_id`, `token`, `control_port`, `egress_port`, `expires_at`). |
| `POST /api/v1/intercept/{id}/stop` | **Token-authenticated** (preferred): the request carries the per-session `token` from the authorize response; honey web hashes it and compares against the stored hash, so no id_token verification or identity resolution is needed — this is what lets teardown keep working under a cluster-scoped identity policy or after the id_token that opened the session has expired. Falls back to id_token authentication (verify the token, resolve the actor, require the actor own the session) when `token` is absent, for compatibility. Either path signals the agent to exit and tears down the session. |

### Policy examples

The brokered path's extra input fields let a policy authorize by SSO group,
exactly like the direct path's `actor` matching, or by any other claim your
provider issues — a department, a team tag, anything present in the
id_token.

Authorize an SSO group into a namespace with `input.groups`:

```rego
package honey
import rego.v1

default allow := false

# anyone in the "platform-eng" SSO group may intercept pods in "staging"
allow if {
	input.action == "intercept"
	"platform-eng" in input.groups
	input.namespace == "staging"
}
```

Authorize on an arbitrary claim with `input.claims.<x>` — for example a
`department` claim your provider includes in the id_token:

```rego
package honey
import rego.v1

default allow := false

# only "payments" department members may intercept the payments namespace
allow if {
	input.action == "intercept"
	input.claims.department == "payments"
	input.namespace == "payments"
}
```

Both examples fail closed exactly like the direct-path example above: no
`allow` for `action == "intercept"` means no ephemeral container is
deployed and no session starts.

### Teardown & TTL

A brokered session ends the same way a direct one does — when `<command>`
exits or you interrupt it, the CLI calls `POST
/api/v1/intercept/{id}/stop`, honey web signals the agent (`SIGTERM`, via
`exec`, using honey's own cluster service-account), and the agent removes
its network redirects before exiting.

If the CLI never gets to call `stop` — it crashes, the machine loses power,
the process is killed — the session would otherwise be orphaned with its
redirects (in particular an `incoming`-mode session's) still active in the
pod. honey web guards against this with a server-side TTL janitor: every
brokered session is bounded by `intercept.session_ttl` (default **1h**) from
the moment it's created, and a background janitor stops any session past its
`expires_at` whether or not the CLI ever calls `stop`.

:::note Known limitation — honey web restart, default `memory` store only
With the default `session_store: memory`, the session registry and its TTL
janitor are **in-memory**. If honey web itself restarts (a deploy, an OOM
kill, a crash) while brokered sessions are live, it loses track of them: the
janitor can only reap sessions it still holds, and — by the [RBAC
split](#the-rbac-split) — only honey's own service account can `exec` into
the agent to signal it, so no operator can tear it down either. Such an
agent (notably an `incoming`-mode one) keeps its redirects until the pod is
deleted. Keep `session_ttl` modest, and prefer draining brokered sessions
before restarting honey web.

Configuring a **`sqlite` or `postgres`** `session_store` removes this
limitation: brokered sessions survive a honey web restart, since the
restarted process's janitor picks up right where the old one left off — see
[Session store](#session-store--restart-durability) below.
:::

### Session store & restart durability

`intercept.session_store` selects where brokered session state lives:

- **`memory`** (default) — an in-process registry. Zero configuration, but
  sessions do not survive a honey web restart (see the note above).
- **`sqlite`** — a local database file, given by `session_store_dsn` as a
  filesystem path. honey creates the file (and its table) on first use and
  keeps that file mode `0600` (owner read/write only). sqlite also writes
  transient sidecar files next to it during writes (a per-transaction journal,
  or `-wal`/`-shm`) that briefly hold the same rows and are created with the
  process umask; honey can't reliably secure those, so for complete at-rest
  protection put the database in a directory only the honey user can read
  (mode `0700`). The stored rows are session metadata (actor, cluster,
  namespace, pod, container) plus a **hash** of the session token — never a
  certificate, key, or id_token.
- **`postgres`** — a shared database, given by `session_store_dsn` as a
  postgres connection string. Use this when more than one honey web replica
  fronts the same clusters, so every replica's janitor sees every session
  regardless of which replica authorized it.

Either persistent option makes the same guarantee: a session authorized by
one honey web process is torn down — by the TTL janitor, or by `/stop` — by
whichever process is running when it needs to be, including one that started
after the process that authorized it exited. The [RBAC
split](#the-rbac-split) is unaffected: only honey's own cluster
service-account ever execs into an agent, whichever process happens to be
running it.

What's persisted is exactly what teardown needs to rebuild an `exec` into
the agent and audit the stop — actor, cluster, namespace, pod, container,
mode, agent image, timestamps — plus a **sha256 hash** of the per-session
token, compared with a constant-time comparison on `/stop`. The plaintext
token, the id_token, and the mTLS device certificate/key are never written
to the store; the plaintext token exists only in the authorize response and
the CLI's own memory. A store DSN (which for postgres may carry credentials)
is never logged.

```yaml
intercept:
  enabled: true
  session_ttl: 1h
  session_store: sqlite
  session_store_dsn: /var/lib/honey/intercept-sessions.db
```

An invalid configuration — an unknown `session_store` value, a `sqlite`/
`postgres` store with no `session_store_dsn`, or a store that fails to
open — fails `honey web` startup outright rather than silently falling back
to the in-memory store.

## Intercept from the web UI

`honey web` can start an interception straight from the browser. In the host
search a **Kubernetes pod** record carries an **Intercept** action; it opens a
small config modal — the interception **modes** (`egress`, `files`, `env`;
`incoming` is shown but disabled, because the browser terminal has no local
`--target` to deliver stolen traffic to), a **UDP** toggle, and an optional
**command** (default `/bin/sh`) — and **Start intercept** opens a browser
terminal whose shell is `mogate`-injected, exactly like `honey intercept <pod>
-- /bin/sh` on the command line.

**Where the shell runs.** The browser terminal is a **direct** intercept
session (not the [brokered](#server-brokered-sso-interception) path), and its
injected shell runs on the **honey-web host** — the machine `honey web` runs
on. Run `honey web` locally (the default operator setup) and it has exactly the
CLI's local semantics: your machine's files and `PATH`, but its egress now
leaving from the pod. (A honey web deployed on a remote server would instead run
that shell server-side; that is a deployment choice, out of scope here.)

**Gated and audited, like the CLI.** A browser interception passes the same
interactive-session OPA gate the [SSH terminal](./ssh-gateway.md) uses, on top of the
[`intercept` gate](#the-intercept-opa-gate) every session enforces, and it
emits the same `intercept_start` / `intercept_stop` [audit](#audit) events. The
actor is the authenticated browser session — never a client-supplied field.

**Concurrency.** Browser interceptions into **different** pods run at the same
time. A second interception into a pod that **already** has one is rejected —
the agent binds fixed in-pod ports and programs a fixed nftables table, so two
in the same pod would collide (the same [one-per-pod](#prerequisites--limits)
rule the CLI has). A configurable cap bounds how many run concurrently:

```yaml
intercept:
  enabled: true
  agent_image: ghcr.io/shareed2k/mogate:0.1.10
  max_sessions: 8          # concurrent browser interceptions (default 8)
```

`intercept.max_sessions` defaults to **8**; a start past the cap is rejected
until an active session ends. The UI lists the active interceptions
(`GET /api/v1/intercept/sessions`) and can stop one
(`POST /api/v1/intercept/sessions/{id}/stop`).

**Teardown.** Clicking the **×** ("Close Terminal") on the terminal's tab
always ends that session immediately and tears the agent down (ephemeral
container, relay, port-forward) — the same teardown the CLI runs when
`<command>` exits — whether or not the session
[resumed](#resume-across-a-browser-refresh) across a refresh. Without a
multiplexer, any dropped WebSocket ends the session the same way, so
refreshing, closing the browser tab, or a network drop all have the same
effect as the × there too. As on the CLI, the ephemeral container entry
itself cannot be removed (see [Prerequisites &
limits](#prerequisites--limits)).

### Resume across a browser refresh

If **tmux** is on the `PATH` of the machine `honey web` runs on, a browser
interception runs inside a tmux pane instead of directly under the
WebSocket, so the session can outlive any one browser tab:

- **Refreshing the browser reattaches, it doesn't restart.** Interception
  into the same pod always resolves to the same tmux session (its name is
  derived from the cluster/namespace/pod, so it's stable across reloads).
  Reload the tab, or close it and reopen the Intercept modal on the same pod,
  and it reattaches to the running pane — same injected shell, same
  environment, same scrollback — instead of deploying a second agent.
- **Two tabs on the same pod share one shell**, never two independent ones.
  Opening a second tab attaches to that same tmux session, so both tabs are
  driving the identical shell process, environment, and scrollback. Only one
  tab is the live, interactive view at a time, though: attaching takes over
  from whichever tab held it, which then shows disconnected — reattaching
  (reopen or refresh that tab) reclaims it. The hand-off itself never
  touches the underlying shell or the ephemeral container — only the ×
  button or Stop does (see below).
- **The session outlives a dropped connection, but not the × button.**
  Refreshing the browser, closing the browser tab or window, or a network
  drop only disconnect the WebSocket — with nothing attached, the pane and
  the agent it drives just keep running, no idle timeout. Only three things
  end it: the injected shell exiting on its own, clicking the **×** ("Close
  Terminal") on the terminal's tab, or **Stop** in the sessions list
  (`POST /api/v1/intercept/sessions/{id}/stop`). The × and Stop both kill
  the tmux session outright — hanging up the pane and running the same
  teardown a normal session exit does, tearing down the ephemeral container,
  the relay, and the port-forward — so either one also ends it for any other
  tab still attached to that same pod's shared shell.
- **As a bonus, not a guarantee:** because the pane belongs to the tmux
  server rather than to the `honey web` process, a resumed session also
  typically survives restarting `honey web` itself (the sessions list, cap,
  and Stop all read tmux directly, so they still see it). This is an
  emergent side effect of running under tmux, not a contract honey commits
  to — don't rely on it, and it does **not** survive a reboot of the honey
  host.
- **Requires tmux on the honey host.** The release image installs it, so a
  container deployment of `honey web` gets resume for free. Running
  `honey web` from a plain binary on a host without tmux on `PATH` still
  intercepts fine, just without resume: every interception (and every
  refresh) uses the one-shot session above, so any disconnect — the ×
  button, a refresh, or closing the browser tab — ends it immediately, and a
  refresh starts a fresh interception rather than reattaching.

## Prerequisites & limits

- **Ephemeral containers** must be available on the target cluster (GA since
  Kubernetes 1.25). Older clusters, or clusters that disable the feature,
  cannot run `honey intercept`.
- **`NET_ADMIN` (as root) + the pod's network namespace.** The deployed agent
  runs as **root** (uid 0) with only the `NET_ADMIN` capability (all others
  dropped) and targets the chosen container's namespaces (`TargetContainerName`)
  so it can install the redirects and tunnels interception needs. Root is
  required: the agent programs nftables via netlink, which needs an *effective*
  `CAP_NET_ADMIN` — a capability added to a non-root container is only
  *permitted*, not effective, unless the image ships file capabilities, so a
  non-root agent fails with `operation not permitted`. Some hardened clusters
  (Pod Security admission, restricted PSPs/PSAs, or an admission webhook) block
  `NET_ADMIN`, root, or ephemeral containers outright — the target namespace
  must permit them (the `privileged` or `baseline`+exception PSA level).
- **RBAC.** On the **direct** path, the identity honey uses to reach the
  cluster (your own kubeconfig) needs, at minimum, every verb below in one
  role, since your own process does the deploy, the token delivery, and the
  port-forward. On the **brokered** path this single role is deliberately
  split across two identities instead — see [The RBAC
  split](#the-rbac-split) above.

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
  delete an ephemeral container once added — it stays a (inert) entry in the
  pod spec, visible in `kubectl describe pod`, after the session ends. On
  teardown honey sends the agent a best-effort `SIGTERM` (via `exec`, over the
  API server — independent of the port-forwards) so it runs its graceful
  shutdown and **removes its network redirects**; without this an incoming
  session's redirects could keep hijacking the pod's traffic. For this to work
  the **agent image must `exec` the agent as PID 1** so it receives the signal.
- **One interception per pod at a time.** The agent uses fixed in-pod ports and
  honey resolves the session's agent as the pod's most-recently-added ephemeral
  container, so two concurrent interceptions on the *same* pod are unsupported
  (they would collide / mis-route). Intercept different pods concurrently
  freely.
- **macOS SIP-restricted binaries are supported (via a re-signed copy).** macOS
  System Integrity Protection makes `dyld` ignore the injector for restricted
  binaries — Apple-signed system binaries such as `/usr/bin/curl` and `/bin/bash`,
  their restricted children (e.g. `bash -c 'curl …'`), and `#!` scripts whose
  interpreter is restricted. honey handles these by running an **ad-hoc-re-signed
  copy** of the binary (never the original): the copy is thinned to its `x86_64`
  slice and run under **Rosetta 2** with an `x86_64` build of the injector, which
  restores injection. Copies live under `~/Library/Caches/mogate/sip/` and
  re-signing *strips* entitlements, so the copy is strictly less privileged than
  the original. Requirements on Apple Silicon: **Rosetta 2 installed**
  (`softwareupdate --install-rosetta`) and a honey build that bundled the
  `x86_64` injector (the release build and `task build` do; a bare `go build`
  does not). If a restricted binary cannot be patched — for example an
  `arm64e`-only binary with no `x86_64` slice, or missing Rosetta — the command
  **fails loud** and is never run un-intercepted.
- **`dig`/`nslookup`/`host` over UDP are not intercepted on macOS.** Interception
  redirects the standard resolver (`getaddrinfo`) and the libc socket calls, so
  ordinary tools and applications resolve cluster names through the agent. BIND's
  DNS utilities (`dig`, `nslookup`, `host`) instead issue their UDP queries via
  raw syscalls that bypass the injected libc symbols, so their **UDP** lookups
  hang. Use TCP DNS, which is intercepted — `dig +tcp <name>`, `nslookup -vc
  <name>`, `host -T <name>` — or any `getaddrinfo`-based check (`curl`,
  `python3 -c 'import socket; socket.gethostbyname("<name>")'`).
- **Sessions are short-lived and always gated.** Every session re-runs the
  [`intercept` gate](#the-intercept-opa-gate) and is fully torn down (agent
  signaled to exit, port-forwards closed, the local session token and
  extracted injector removed) when `<command>` exits or the session is
  interrupted — there is no persistent or background interception left
  running after `honey intercept` returns.

## From a CUE recipe

The same interception is also available as an `intercept` step in a
[CUE recipe](./cue-recipes.md), targetless, so a playbook can give a local
`command`/`script` egress and environment access into a cluster without the
CLI or a terminal session — see [CUE Recipes — Intercept
steps](./cue-recipes.md#intercept-steps).
