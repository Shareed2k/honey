---
id: cue-recipes
title: CUE Recipes
slug: /cue-recipes
---

Honey can run multi-step playbooks defined in [CUE](https://cuelang.org/). Each step targets hosts from your current search and performs **exactly one** action: `command`, `put`, `get`, `script`, `agent_transfer`, `summarize`, `ai`, `plugin` (WASM — see [Plugin development](./plugins-development.md)), `tunnel` (operator-side port forward), or `k8s` (Kubernetes API — see [Kubernetes steps](#kubernetes-steps)).

Use **`honey cue-validate`** to check a file, **`honey cue-exec`** to dry-run or execute (same host resolution as `honey search`). From the search TUI, press **r** (append `!` to the path to execute). The [Web UI](./web-ui.md) Recipes tab runs the same engine.

**Example recipes:** [`examples/recipe/`](https://github.com/shareed2k/honey/tree/main/examples/recipe) on GitHub (see that folder’s README for a full file index).

## Quick start

```bash
# Validate schema
honey cue-validate examples/recipe/graph_when.cue

# Dry-run plan (default)
honey cue-exec examples/recipe/graph_when.cue my-filter

# Execute over SSH
honey cue-exec --execute examples/recipe/graph_when.cue
```

CLI details: [`honey cue-exec`](./cli/honey_cue-exec.md), [`honey cue-validate`](./cli/honey_cue-validate.md).

## Host matching

Each step has a `host` field resolved against search results:

| `host` value | Meaning |
|--------------|---------|
| Exact name | Case-insensitive match on `Name` |
| Literal IP | Match `PrimaryIP` |
| `"*"` | Every row with a `PrimaryIP` |
| `"re:PATTERN"` | Go regexp (RE2) on `Name` (rows with IP only) |
| `"_"` | Local only — required for `summarize`/`ai` steps |

For **`agent_transfer`**, `host` is the **source**; `agent_transfer.dest_host` selects the destination (each must match exactly one row).

## Step kinds

| Kind | Remote? | Notes |
|------|---------|-------|
| `command` | SSH / k8s exec | Optional `env`, `secrets`, `hooks`, `kv_tunnel`, `templated` — [Templated command/script steps](#templated-commandscript-steps) |
| `script` | SSH / k8s exec | Upload `local` → `remote`, then `sh <remote>`; optional `templated` |
| `plugin` | SSH / k8s exec | Custom step (WASM or `runtime: docker`) — [Plugin development](./plugins-development.md), optional `kv_key`/`kv_key_per_host` |
| `tunnel` | SSH / k8s / TrueNAS | Operator-side listen (local/remote/dynamic/UDP/tun) — [Tunnel steps](#tunnel-steps) |
| `k8s` | Kubernetes API | Direct API calls: apply, delete, scale, rollout, get, exec, job — [Kubernetes steps](#kubernetes-steps) |
| `put` / `get` | SFTP or k8s tar stream | Relative `local` paths from recipe directory |
| `agent_transfer` | A→cloud→B | Needs honey config for `cloud_backend_ref` |
| `summarize` | Local (operator) | `host: "_"`; terminal step only — must be last, at most one per recipe, nothing may depend on it; summarizes the whole run/ancestor chain; needs `OPENAI_API_KEY` when executing |
| `ai` | Local (operator) | `host: "_"`; a single LLM completion, usable anywhere (any position, any number of times, other steps may depend on its output) — like `template:` but calling an LLM instead of rendering a template; optional `templated`; needs `OPENAI_API_KEY` when executing |
| `recipe` | Local (operator) | Invoke a sub-recipe — [Sub-recipes](#sub-recipes) |

Optional **`recipe.defaults`**: `run_as`, `env`, `secrets`, `kv_tunnel`, `max_parallel`, `ssh_port`, `ssh_private_key`, `k8s_debug_image`.

## Sub-recipes

You can invoke a CUE recipe from within another recipe using the `recipe` step kind. This allows you to build reusable modules (e.g., `setup-postgres.cue`) and orchestrate them from a parent playbook.

- The `path` is resolved relative to the parent recipe's directory.
- You can pass inputs to the child recipe via the `prompts` map.
- **Targeting**: The parent step dictates the target hosts. Any `host:` filters inside the child recipe are ignored, and all its steps execute over the hosts passed down from the parent.

```cue
{
  id: "setup-db"
  host: "db-*"
  recipe: {
    path: "setup-postgres.cue"
    prompts: {
      PG_VERSION: "16"
    }
  }
}
```

### SSH customization

`ssh_port` and `ssh_private_key` can be set at `recipe.defaults` (all steps) or on a single step (overrides defaults). `~/` is expanded in `ssh_private_key`.

```cue
recipe: {
  name: "with-key"
  defaults: {
    ssh_private_key: "~/.ssh/my_staging_key"
  }
  steps: [{
    host:    "*"
    command: "hostname"
  }, {
    host:            "*"
    ssh_private_key: "~/.ssh/other_key" // this step uses a different key
    command:         "whoami"
  }]
}
```

Example: [`with_ssh_key.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/with_ssh_key.cue).

Remote env injection (command/script/plugin): `HONEY_HOST_NAME`, `HONEY_HOST_PRIMARY_IP`, `HONEY_HOST_PROVIDER`, `HONEY_HOST_ZONE`, `HONEY_HOST_REGION`, `HONEY_HOST_META_*` from host metadata, and `HONEY_VAR_*` from resolved inventory variables.

## Linear vs graph execution

**Default (linear):** steps run in array order, one after another.

**Graph mode:** set `recipe.type: "graph"`. Steps need unique **`id`** values and optional **`depends: [id, ...]`** forming a DAG. Honey runs **waves** — all steps in a wave may run concurrently (default up to 8 steps per wave; host-level concurrency is capped by `max_parallel`, default 32).

```cue
recipe: {
  name: "parallel-restart"
  type: "graph"
  steps: [
    { id: "fetch", host: "*", command: "echo fetch" },
    { id: "restart_a", host: "*", depends: ["fetch"], command: "echo a" },
    { id: "restart_b", host: "*", depends: ["fetch"], command: "echo b" },
    { id: "verify", host: "*", depends: ["restart_a", "restart_b"], command: "echo ok" },
  ]
}
```

### Graph extras

**`env_from`** maps a dependency step’s output into env vars on the current step (per host). Each entry must reference a step in **`depends`**. Supported on `command`, `script`, and `plugin` only.

Each `env_from` entry uses **one** of these source fields:

| Field | Source | Description |
|-------|--------|-------------|
| `map: {KEY: "stdout"}` | Step stdout | Map raw stdout to `KEY` |
| `extract: {KEY: ".field"}` | Step stdout (JSON) | JQ path extracted from JSON stdout |
| `kv: {KEY: "kv_key_name"}` | Recipe KV store | Pull value written by a prior remote step |
| `from_output: "NAME"` | Template output | Named capture from a `template` step on `host: "_"` |

```cue
// Three env_from sources in one step
{
  id:      "pg_followup"
  host:    "db-*"
  depends: ["pg_query"]
  env_from: [{
    step: "pg_query"
    map:     { RAW_OUT: "stdout" }          // raw stdout
    extract: { COUNT: ".[0].n" }            // jq on JSON stdout
  }, {
    kv: { THRESHOLD: "pg_activity_count" } // value written by a remote step
  }]
  command: "echo count=$COUNT threshold=$THRESHOLD"
}
```

Example: [`postgres_kv_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_kv_demo.cue).

- **`HONEY_STEP_ID`:** set on remote env when the step has an `id` (use to namespace shared KV keys).
- **`kv_tunnel`:** one operator-side `stepkv` session for the whole run; see [KV tunnel](#recipe-kv-tunnel) below.
- **Failure / skip:** if a step fails (or all hosts hit transient SSH errors), **descendants are skipped**. If a **`summarize`** step becomes unreachable, the run **aborts**.
- **Web UI:** Recipes wizard → **Graph** tab shows the DAG (`POST /api/v1/recipes/validate-content` → `graph` field).

Example: [`graph_parallel.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/graph_parallel.cue).

## Step-level assertions

You can assert that a step succeeded based on its output or a specific exit code. When an assertion fails, the step is marked as failed even if the exit code was 0.

Supported assertions inside the `assert: []` block:
- **`exit_code`**: Overrides the default success check. If the command exits with this code, the step succeeds (even if the code is non-zero).
- **`regex`**: Verifies `stdout`/`stderr` matches the regular expression.
- **`not_regex`**: Verifies the output DOES NOT match the regular expression.
- **`json_path`**: Parses the output as JSON and verifies the given `tidwall/gjson` path exists.
- **`expected_value`**: Used alongside `json_path` to verify the extracted JSON field strictly matches this string.

```cue
recipe: {
  name: "assertions-demo"
  steps: [
    {
      host: "local"
      command: "curl -s http://localhost/health"
      assert: [{
        json_path: "status"
        expected_value: "healthy"
      }]
    }
  ]
}
```

## Conditional steps (`when` + CEL)

Optional **`when: "<CEL expression>"`** on any step kind. The expression must evaluate to **bool**. When false, that target is **skipped** without SSH/SFTP:

| Kind | When evaluated |
|------|----------------|
| `command`, `script`, `plugin`, `put`, `get` | Per expanded target host |
| `agent_transfer` | Once on the **source** host (`dest.*` available in CEL) |
| `summarize`, `ai` | Once locally (`host.name == "_"`; `steps` uses aggregated prior results) |

### Rules

- **`id` is required** whenever `when` is set (linear or graph).
- **Linear recipes:** `id` is only allowed on steps that have `when`.
- **Graph:** step ids referenced in `when` (e.g. `steps['fetch']`) must appear in that step’s **`depends`**.
- Expressions are compiled at **validate** time ([CEL](https://github.com/google/cel-spec)); max length 4 KiB.
- If **every** target for a graph step is when-skipped, the step is treated as **skipped** and **dependents are skipped** (same as a failed branch).

### CEL variables and functions

| Name | Type | Meaning |
|------|------|---------|
| `host.name`, `host.ip`, `host.provider`, `host.zone`, `host.region` | string | Current target host |
| `host.meta` | map | Host metadata (`Record.Meta` from search) |
| `host.extra_ips` | list | Extra IPs |
| `dest.name`, `dest.ip`, … | same as `host` | Destination host (`agent_transfer` only) |
| `steps['id'].succeeded` | bool | Prior step outcome on this host |
| `steps['id'].skipped` | bool | Prior step was skipped |
| `steps['id'].stdout` | string | Captured stdout (`command` / `script` / `plugin`) |
| `steps['id'].exit_code` | int | Remote exit code |
| `secrets['KEY']` | string | Only keys in `defaults.secrets` / `step.secrets` |
| `execute` | bool | `false` on dry-run / plan |
| `recipe_name` | string | Recipe `name` |
| `vars` | map | Resolved inventory variables for the current host |
| `groups` | list | Resolved inventory group names for the current host |
| `in_group(name)` | bool | True if the host matches the specified inventory group |
| `kv_get(key)` | string | Operator recipe KV (`""` if missing) |
| `kv_has(key)` | bool | Whether key exists in recipe KV |

### Examples

**Prior stdout (graph):**

```cue
{
  id: "deploy"
  host: "*"
  depends: ["fetch"]
  when: "steps['fetch'].stdout.contains('shard')"
  command: "echo deploy on $HONEY_HOST_NAME"
}
```

**Recipe KV** (requires `defaults.kv_tunnel` or per-step `kv_tunnel`; keys must **not** contain `/`):

```cue
when: "kv_has('graph_seed_' + host.name + '_ready')"
```

**Declared secrets** (resolved on `--execute`; dry-run uses `<<secret …>>` placeholders):

```cue
when: "secrets['FLAG'] != ''"
```

Files: [`graph_when.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/graph_when.cue), [`graph_when_kv.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/graph_when_kv.cue), [`graph_when_secrets.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/graph_when_secrets.cue).

### Security

- **`secrets` in CEL** use the same resolver as recipe `step.secrets` — treat them as equally sensitive; they resolve on the operator machine running honey.
- **`kv_get` / `kv_has`** read **operator-local** stepkv state for the run (what prior steps wrote via `HONEY_KV_URL` on remotes). They do not read arbitrary remote paths from the laptop.
- Dry-run plans and assist output must not show resolved secret values.

Skipped hosts appear in results with **`Skipped: true`** and output `(skipped: when)`.

## Kubernetes steps

A **`k8s:`** step calls the Kubernetes API directly using `client-go` — no `kubectl` binary required. It targets k8s host records (`provider == "k8s"`) and reads credentials from the host's metadata fields (`kubeconfig`, `kube_context`, `namespace`). The step's optional `namespace` field overrides the host meta value.

**Exactly one** action field must be set per step. No new binary dependencies — `k8s.io/client-go` is already bundled with honey.

### Action reference

| Action | Description |
|--------|-------------|
| `apply` | Apply a YAML/JSON manifest via server-side apply |
| `delete` | Delete a resource by `kind/name` |
| `scale` | Set replica count on a Deployment, StatefulSet, or ReplicaSet |
| `rollout_restart` | Trigger rolling restart by patching the restart annotation |
| `wait` | Poll until a resource condition is true |
| `get` | Fetch a resource or list by label selector; writes JSON/YAML to stdout |
| `exec` | Run a command in an existing pod container (no ephemeral container) |
| `create_job` | Create a batch Job and optionally wait for completion |

### Rollout restart

```cue
recipe: {
  name: "k8s-rollout-restart"
  steps: [{
    host: "re:provider==k8s"
    k8s: {
      namespace: "production"
      rollout_restart: {
        resource: "deployment/api"
        wait:     true
      }
    }
  }]
}
```

`wait: true` polls until all pods are updated and available (times out after 10 minutes). Supported resources: `deployment`, `statefulset`, `daemonset`.

Example: [`k8s_rollout_restart.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/k8s_rollout_restart.cue).

### Scale

```cue
recipe: {
  name: "k8s-scale"
  type: "graph"
  steps: [
    { id: "down", host: "re:provider==k8s", k8s: { scale: { resource: "deployment/worker", replicas: 0 } } },
    { id: "up",   host: "re:provider==k8s", depends: ["down"], k8s: { scale: { resource: "deployment/worker", replicas: 3 } } },
  ]
}
```

Supported resources: `deployment`, `statefulset`, `replicaset`. For other scalable resources use `apply` with a patched replica count.

Example: [`k8s_scale.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/k8s_scale.cue).

### Wait

```cue
{
  host: "re:provider==k8s"
  k8s: {
    wait: {
      resource: "deployment/api"
      "for":    "condition=Available"
      timeout:  "5m"
    }
  }
}
```

`for` format: `condition=<ConditionType>` (e.g. `condition=Available`, `condition=Ready`). Timeout is a Go duration string (`"2m"`, `"90s"`); default 5 minutes.

### Get (with graph capture)

`k8s.output` stores the action result under a named key in the graph capture store, available to downstream steps via `env_from[].from_output` or `env_from[].step`:

```cue
recipe: {
  name: "k8s-get-pods"
  type: "graph"
  steps: [
    {
      id:   "get_pods"
      host: "re:provider==k8s"
      k8s: {
        namespace: "production"
        output:    "pods_json"
        get: {
          resource:       "pods"
          label_selector: "app=api"
          format:         "json"
        }
      }
    },
    {
      id:      "report"
      host:    "_"
      depends: ["get_pods"]
      env_from: [{
        step: "get_pods"
        map: PODS_JSON: "stdout"
      }]
      template: {
        template: "Pods:\n{{ .PODS_JSON }}\n"
        data:     {}
      }
    },
  ]
}
```

`format` values: `json` (default), `yaml`, `name`. `output` is only valid on `get`, `exec`, and `create_job`.

Example: [`k8s_get_pods.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/k8s_get_pods.cue).

### Exec

Runs a command in an existing pod container via the k8s exec subresource (SPDY). Does not create an ephemeral debug container.

```cue
{
  host: "re:provider==k8s"
  k8s: {
    namespace: "production"
    exec: {
      pod:       "api-7d6f8b9c4-xk2pq"
      container: "api"
      command:   ["cat", "/etc/config/app.yaml"]
    }
  }
}
```

`container` is optional (uses the first container). `tty: true` allocates a pseudo-TTY (for interactive shells; not useful in automated recipes).

Example: [`k8s_exec.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/k8s_exec.cue).

### Create Job

```cue
{
  host: "re:provider==k8s"
  k8s: {
    namespace: "production"
    create_job: {
      name:        "db-migrate"
      image:       "my-app:v2.3.0"
      command:     ["/app/migrate"]
      args:        ["--target=latest"]
      env: {
        DATABASE_URL: "postgres://db.internal:5432/app"
        LOG_LEVEL:    "info"
      }
      restart_policy: "Never"
      wait:           true
      ttl_seconds:    600
    }
  }
}
```

`wait: true` polls until the job completes or fails (30 minute timeout); on completion, job pod logs are written to stdout. `ttl_seconds` configures automatic cleanup after job completion (`TTLSecondsAfterFinished`).

Example: [`k8s_create_job.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/k8s_create_job.cue).

### Apply

Applies a YAML/JSON manifest via server-side apply (`FieldManager: "honey"`):

```cue
{
  host: "re:provider==k8s"
  k8s: {
    apply: {
      manifest: """
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: app-config
          namespace: production
        data:
          LOG_LEVEL: info
        """
      server_side: true
    }
  }
}
```

`force: true` maps to `--force-conflicts` (overwrites field manager conflicts). The namespace in the manifest takes precedence over the step `namespace` field.

Example: [`k8s_apply_manifest.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/k8s_apply_manifest.cue).

### Delete

```cue
{
  host: "re:provider==k8s"
  k8s: {
    delete: {
      resource: "job/db-migrate"
      wait:     true
    }
  }
}
```

`wait: true` polls until the resource is gone (5 minute timeout). `IsNotFound` is treated as success so re-running the step is idempotent.

### Host metadata fields

k8s host records must have the following metadata to connect:

| `host.Meta` key | Required | Description |
|-----------------|----------|-------------|
| `kubeconfig` | No | Path to kubeconfig file; uses default rules if absent |
| `kube_context` | No | kubeconfig context to activate |
| `namespace` | No | Default namespace; overridden by `k8s.namespace` field |

The `k8s` provider auto-populates these when hosts are discovered from cluster inventory.

### Limitations

- `k8s.output` is only valid on `get`, `exec`, and `create_job` actions.
- `run_as` is not supported on `k8s` steps.
- `apply` always uses server-side apply; `force` resolves field manager conflicts.
- `scale` supports Deployment, StatefulSet, ReplicaSet only; use `apply` for other scalable resources.
- `rollout_restart` supports Deployment, StatefulSet, DaemonSet only.
- `wait` supports `condition=<Type>` format only; arbitrary JSONPath conditions are not yet supported.
- Helm operations are out of scope — use the Helm plugin instead.

## Tunnel steps

A **`tunnel:`** step opens a TCP/UDP listen address (or tun device) on the **operator** — the machine where `honey cue-exec` runs. Honey dials the recipe target (SSH, k8s port-forward API, or TrueNAS API shell) and forwards traffic to a service on **remote loopback** or inside a pod.

Use tunnels for Redis, HTTP APIs, Postgres (via the postgres plugin), **rclone rcd** (via the rclone plugin), SOCKS browsing through a bastion, or any protocol that fits the forward mode. The listen socket is always on the operator (`127.0.0.1:<port>` by default), not on the remote host.

### Quick start (SSH local forward)

```cue
recipe: {
  name: "redis-tunnel"
  steps: [{
    host: "cache-*"
    tunnel: {
      remote_host: "localhost"
      remote_port: 6379
    }
  }]
}
```

```bash
honey cue-validate examples/recipe/tunnel_local_forward.cue
honey cue-exec examples/recipe/tunnel_local_forward.cue "cache-*"          # plan
honey cue-exec --execute examples/recipe/tunnel_local_forward.cue "cache-*"  # open tunnel
```

On **`--execute`**, step stdout is JSON:

```json
{"host":"127.0.0.1","port":54321,"mode":"local","remote_host":"localhost","remote_port":6379}
```

Connect from the **same machine running honey** (e.g. `redis-cli -h 127.0.0.1 -p 54321`).

Example with a hold step so the tunnel stays up while you debug: [`tunnel_local_forward.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_local_forward.cue).

### Field reference

| Field | Meaning |
|-------|---------|
| `mode` | `local` (default, SSH `-L`), `remote` (`-R`), `dynamic` (SOCKS5), `udp`, `tun` (`ssh -w`, L3 only) |
| `remote_host` / `remote_port` | Remote side of a local forward (default host `localhost`) |
| `local_port` | Operator listen port (`0` or omitted = auto) |
| `bind` | Operator bind address (loopback only unless `tunnels.allow_non_loopback_bind` in honey config) |
| `remote_bind` / `remote_listen_port` / `local_host` / `local_target_port` | Remote forward (`mode: "remote"`) |
| `use_ssh_config` | Pick `LocalForward` / `RemoteForward` from `~/.ssh/config` via `ssh -G` |
| `ssh_config_match` | Optional match on remote port when multiple forwards exist |
| `ssh_config_env` | Env vars passed to `ssh -G` (for `Match exec` predicates) |
| `share_key` | Reuse the same operator listen port when multiple steps or hosts acquire the same tunnel in one run (process-wide pool) |
| `protocol` | `udp` (with `mode: "udp"`) |
| `remote_socat` | Required `true` for UDP mode (bootstraps `socat` on the remote) |
| `tun_local` / `tun_remote` | Tun interface ids for `mode: "tun"` |

**Provider dispatch:** k8s pod targets → Kubernetes port-forward; TrueNAS API-shell hosts → TrueNAS tunnel backend; everything else → SSH.

Dry-run prints placeholder JSON (`<<127.0.0.1>>`, `<<port>>`) and annotates ssh_config source when `use_ssh_config` is set.

### Modes

**Local forward (default)** — reach one TCP endpoint from the operator; `remote_host` is dialed **from the SSH target’s network** (use a hostname reachable from the bastion, not only `localhost`):

```cue
// Bastion can reach db.internal; operator gets one TCP port
{
  host: "bastion-*"
  tunnel: { remote_host: "db.internal", remote_port: 5432 }
}
```

**SOCKS5 (many internal hosts)** — see [Jump host → many internal services (SOCKS)](#jump-host--many-internal-services-socks) below.

**UDP relay (internal DNS)** — OpenSSH `-L` is TCP-only. For DNS, syslog, SNMP, etc., use `mode: "udp"` with **`remote_socat: true`** (starts `socat` on the SSH target). Example: [`tunnel_udp_dns.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_udp_dns.cue).

```cue
tunnel: {
  mode:         "udp"
  bind:         "127.0.0.1"
  local_port:   1053
  remote_host:  "10.96.0.10"   // kube-dns ClusterIP, or dc-dns.internal
  remote_port:  53
  remote_socat: true
}
```

```bash
honey cue-exec --execute examples/recipe/tunnel_udp_dns.cue "k8s-worker-*"
dig @127.0.0.1 -p 1053 kubernetes.default.svc.cluster.local
```

Requires **`socat`** on the SSH target.

**DNS from a k8s pod** — port-forward to a **CoreDNS pod** (TCP only; UDP is not supported by the k8s API). Use normal `tunnel` (not `mode: "udp"`) with `remote_port: 53`, then `dig +tcp`:

```bash
honey cue-exec --execute examples/recipe/tunnel_k8s_dns_tcp.cue "k8s:coredns-xxxxx"
dig @127.0.0.1 -p 1053 +tcp kubernetes.default.svc.cluster.local
```

To query cluster DNS **without** tunneling to your laptop, run `dig` in a debug pod via a `command` step (`host: "k8s:…"`). See [`tunnel_k8s_dns_tcp.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_k8s_dns_tcp.cue).

**L3 tun (`ssh -w`)** — point-to-point tunnel to a private subnet (e.g. `10.48.0.0/16` only reachable from a DC gateway). Example: [`tunnel_tun_datacenter.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_tun_datacenter.cue).

```cue
tunnel: { mode: "tun", tun_local: 0, tun_remote: 0 }
```

Stdout has **`tun_name`** (e.g. `tun0`), not a TCP port. Honey does **not** configure IP addresses or routes — after `--execute`:

```bash
sudo ip link set tun0 up
sudo ip addr add 10.255.0.2/30 dev tun0
sudo ip route add 10.48.0.0/16 dev tun0
```

Requires **root or `CAP_NET_ADMIN`** on the operator, **`PermitTunnel yes`** (or equivalent) on the remote sshd, and matching tun/routing config on the gateway (see example file header).

**ssh_config forwards** — no `remote_port` when `use_ssh_config: true`:

```cue
tunnel: {
  use_ssh_config: true
  ssh_config_match: "5432"
  ssh_config_env: { ROLE: "prod" }
}
```

**k8s pod** — `host: "k8s:my-pod"`, `tunnel: { remote_port: 5432 }`. See [`postgres_tunnel_k8s.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_tunnel_k8s.cue).

Example index: [`tunnel_local_forward.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_local_forward.cue) (TCP), [`tunnel_socks.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_socks.cue) (SOCKS5), [`tunnel_udp_dns.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_udp_dns.cue) (UDP), [`tunnel_tun_datacenter.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_tun_datacenter.cue) (tun).

### Jump host → many internal services (SOCKS)

When you have SSH to a **bastion** but need to reach **many** internal hosts (Grafana, Jenkins, several DBs, etc.), use **`mode: "dynamic"`** on one tunnel step. Honey opens **SOCKS5 on the operator**; traffic exits from the bastion’s network, so you can reach any host/port the bastion can reach without a separate tunnel per backend.

Flow: **client on operator** → `127.0.0.1:1080` (SOCKS5) → SSH dynamic forward (`-D`) → **bastion** → internal host B, C, D…

Example: [`tunnel_socks.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_socks.cue)

```cue
{
  host: "bastion-*"
  tunnel: {
    mode:       "dynamic"
    bind:       "127.0.0.1"
    local_port: 1080
  }
}
```

```bash
honey cue-exec --execute examples/recipe/tunnel_socks.cue "bastion-*"

# HTTP (DNS resolved on the bastion)
curl --socks5-hostname 127.0.0.1:1080 http://grafana.internal:3000/api/health

# TCP clients via proxychains (Postgres, mysql, etc.)
proxychains4 psql 'host=db.internal port=5432 user=ro dbname=app sslmode=require'
```

In Firefox: SOCKS v5 → `127.0.0.1:1080`, enable **Proxy DNS when using SOCKS v5** so internal names resolve on the bastion.

| Pattern | Recipe config | Operator endpoint | Reach |
|---------|---------------|-------------------|--------|
| **SOCKS (many backends)** | `mode: dynamic` on bastion | `127.0.0.1:1080` SOCKS5 | Any host/port reachable from bastion |
| **Local forward (one backend)** | `remote_host` + `remote_port` on bastion | `127.0.0.1:<port>` TCP | One fixed host:port |
| **Postgres `tunnel_step`** | local forward + `tunnel_step` | pgx DSN rewrite | One TCP service only |

Use **SOCKS** when you need multiple internal destinations through one jump host. Use **local forward** when you want one stable TCP port (e.g. postgres **`tunnel_step`**).

**Postgres caveat:** the postgres WASM plugin rewrites DSN to a **TCP** listen address; it does not speak SOCKS. For Postgres through a bastion SOCKS proxy, use **`proxychains`** (or similar) with `psql`/`host_exec`, or a **local forward** to a specific `db.internal:5432` instead of `mode: dynamic`.

### Lifecycle

Tunnels stay open for the **entire `cue-exec` run**. When the run exits, honey releases pool references and closes forwards.

To keep a tunnel up while you connect manually:

1. Add a follow-up step that blocks (e.g. `command: "sleep 300"` on the target), or
2. Use **graph mode** so dependent steps extend the run before it finishes.

`share_key` deduplicates tunnels **within a run** (and via the process-wide pool when multiple acquisitions use the same key). It does not leave a tunnel open after `cue-exec` exits unless another process still holds a reference.

### Graph mode and `env_from`

Tunnel stdout is recorded like command/plugin stdout. In **graph** recipes, a dependent step can map values via **`env_from`** + **`extract`** (jq on the JSON stdout):

```cue
{
  id: "probe"
  host: "*"
  depends: ["api_tunnel"]
  env_from: [{
    step: "api_tunnel"
    extract: { TUNNEL_PORT: ".port" }
  }]
  command: "echo tunnel port is $TUNNEL_PORT"
}
```

Note: `TUNNEL_PORT` is the **operator** listen port — useful for local `template` steps (`host: "_"`) or documentation; remote commands cannot reach `127.0.0.1` on the operator.

### Postgres integration

The postgres WASM plugin can rewrite a sealed DSN to the tunnel endpoint via **`tunnel_step`** (see [Plugin development — Postgres](./plugins-development.md#postgres_query--postgres_exec)). Examples: [`postgres_tunnel_demo.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_tunnel_demo.cue), [`postgres_tunnel_ssh_config.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/postgres_tunnel_ssh_config.cue).

## Recipe KV tunnel

Every **`cue-exec --execute`** run starts one in-memory **stepkv** session on the operator and exposes it to remotes via `HONEY_KV_URL` and `HONEY_KV_TOKEN` on command/script/plugin steps. The recipe fields `defaults.kv_tunnel` / `step.kv_tunnel` are deprecated no-ops.

- **Keys:** single path segment — no `/` in the key string (use underscores, e.g. `graph_${HONEY_STEP_ID}_${HONEY_HOST_NAME}_ready`).
- **API:** `PUT` / `GET` / `DELETE` `/v1/kv/{key}`, `GET /v1/kv/__health`.
- **Graph mode:** one shared session for the whole run; namespace per step/host to avoid races in the same wave.

See [`honey cue-exec` — kv_tunnel](./cli/honey_cue-exec.md) and [`kv_tunnel_multistep_example.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/kv_tunnel_multistep_example.cue).

## Retry

Steps can be retried automatically on failure. Set `retry` at `recipe.defaults` (applies to all steps) or on individual steps (overrides the default).

| Field | Default | Description |
|-------|---------|-------------|
| `attempts` | `3` | Total attempts (1 = no retry) |
| `delay_ms` | `1000` | Initial delay between attempts in ms |
| `max_delay_ms` | `30000` | Maximum delay cap (for exponential backoff) |
| `backoff` | `"fixed"` | `"fixed"` or `"exponential"` |

```cue
recipe: {
  name: "restart-with-retry"
  type: "graph"
  defaults: {
    retry: { attempts: 3, delay_ms: 2000, backoff: "exponential" }
  }
  steps: [{
    id:      "verify"
    host:    "*"
    // per-step override: poll up to 30 times, fixed 2 s gap
    retry:   { attempts: 30, delay_ms: 2000, backoff: "fixed" }
    command: "service my-app status | grep -q running"
  }]
}
```

Example: [`kafka_controller_rolling_restart.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/kafka_controller_rolling_restart.cue) uses `attempts: 30, delay_ms: 2000, backoff: "fixed"` on the verify step.

## Secrets and env

- **`env`:** literal `KEY=value` maps on command/script/plugin (and remote hooks).
- **`secrets`:** values must be `secure:v1:…` refs; decrypted at execute time. See [examples/recipe README — Secrets authoring](https://github.com/shareed2k/honey/blob/main/examples/recipe/README.md#secrets-authoring).
- CLI **`-e KEY=value`** overrides recipe env on duplicate keys (command/script only).

## Hooks, notify, and AI

- **Hooks** (`on_success` / `on_failure`): command/script/plugin only; local or remote follow-up per host. See [`honey cue-exec`](./cli/honey_cue-exec.md).
- **`notify`:** optional per-step notifications after success ([nikoksr/notify](https://github.com/nikoksr/notify)).
- **`summarize`:** terminal summarizer after prior steps — must be the last step in the recipe, at most one per recipe, and no other step may depend on it; sends the whole run's (or, in graph mode, its full ancestor chain's) combined output to the model in one request; optional `notify` and `when` (aggregated `steps` view).
- **`ai`:** a single LLM completion, usable like any other step — any position, any number of times, and other steps may consume its output via `output`/`env_from`/`from_output` just like `template:`. Unlike `summarize:`, it has no built-in transcript aggregation; the prompt is sent as written, or rendered from prior step/KV data first via `templated: true` (see [Templated command/script steps](#templated-commandscript-steps) for the function reference — `ai:` reuses the same rendering). Optional `notify` and `when`.

## Loops and Matrix Execution

Steps can be repeated dynamically over a list of items using the `loop`, `loop_from`, or `matrix` fields.

### Matrix Execution

You can fan-out a single step into multiple independent steps running in parallel using a **Cartesian matrix**. 

The engine expands the step before execution. For example, the following matrix will create 4 parallel steps (`postgres v1`, `postgres v2`, `mysql v1`, `mysql v2`) and inject the variables into the step's environment:

```cue
{
  id: "deploy-db"
  host: "local"
  matrix: {
    db: ["postgres", "mysql"]
    version: ["v1", "v2"]
  }
  command: "echo 'Deploying \(env.db) \(env.version)'"
}
```

If a downstream step references a matrix step via `env_from`, the engine will aggregate all outputs from the expanded matrix nodes into a **JSON array** string (e.g. `["out1", "out2", "out3", "out4"]`), which you can parse with `extract` (jq).

### `loop` and `loop_from`

- **`loop`:** A Go text/template string (e.g. evaluating prior step stdout as a JSON array using `stepStdoutLines`) or a JQ expression.
- **`loop_from`:** Selects a prior step and extracts a JSON array using a JQ path.

### Hook Failures in Loops

By default, step hooks (`on_success` or `on_failure`) run as side-car post-facto tasks and their outcome does not affect the main step's success status. However, **if a step hook fails within a loop**, the loop execution immediately **aborts** and the parent step **fails** (unless `ignore_errors` is set to `true`). This prevents subsequent loop iterations from running after a deployment or verification hook encounters an error.

## Template functions

`template` steps use Go [`text/template`](https://pkg.go.dev/text/template) with the full [slim-sprig](https://go-task.github.io/slim-sprig/) function library (string, math, date, crypto, etc.) plus two recipe-specific functions:

| Function | Returns | Description |
|----------|---------|-------------|
| `kvGet "key"` | string | Read a value from the recipe KV store (`""` if missing) |
| `kvHas "key"` | bool | Check whether a key exists in the recipe KV store |

```cue
// Local template reads a value written by a prior remote step
{
  id:      "render"
  host:    "_"
  depends: ["write_kv"]
  template: {
    template: "status={{ kvGet \"deploy_status\" | default \"unknown\" }}\n"
    output:   "RESULT"
  }
}
```

**`template.output`** captures the rendered string under a named key. It is only supported on `host: "_"` (local steps). For per-host templates (`host: "*"`), omit `output` and pass the result downstream via `env_from[].from_output` instead.

Example: [`template_kv.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/template_kv.cue).

## Templated command/script steps

`env_from` caps every value at **8192 bytes** (`command`/`script` steps only ever see prior-step data as `$VAR` shell env vars). For larger payloads — a fetched web page, a large query result — set **`templated: true`** on a `command` or `script` step instead: its body is rendered as a Go template *before* the step runs, with the same `kvGet`/`kvHas`/sprig functions `template:` steps use, plus two more:

| Function | Returns | Description |
|----------|---------|-------------|
| `stepStdout "id"` | string | First non-empty stdout captured for a prior step id (any host) |
| `stepStdoutLines "id"` | []string | Same, split into lines |
| `outputStdout "name"` | string | Value captured by a `template.output`/`k8s.output`-style named capture |
| `outputStdoutLines "name"` | []string | Same, split into lines |

The template `Data` also exposes `.steps`, `.outputs`, and (for `command` steps only — see below) `.env`:

```cue
{
  id:        "check-title"
  host:      "*"
  depends:   ["fetch-protected-site"]
  templated: true
  command: """
    TITLE="{{ regexFind "(?i)<title>.*</title>" (kvGet "stealth_fetch" | fromJson).content }}"
    echo "Fetched page: $TITLE"
    if [ -z "$TITLE" ]; then
      echo "no title found" >&2
      exit 1
    fi
  """
}
```

Pairs naturally with a plugin step's **`kv_key`** (see [Plugin development](./plugins-development.md#kv_key--kv_key_per_host)), which writes the plugin's stdout straight to the recipe KV store (65536-byte cap) instead of `env_from` — the pattern the example above uses (`kvGet "stealth_fetch" | fromJson` reads back a plugin's JSON output written via `plugin.kv_key: "stealth_fetch"`).

Rules and caveats:

- **`command` steps** get per-host `.env` (the same resolved env `$VAR` expansion already sees) in addition to `.steps`/`.outputs`/KV. **`script` steps** render the *local file's content* once, before it's uploaded to any host — since one file is shared across every target, `.env` is not available there, only `.steps`/`.outputs`/KV.
- Template **syntax** is checked at `cue-validate` time (a malformed `{{ }}` fails validation immediately). The template is **not** actually rendered during a dry-run (`cue-exec` without `--execute`) — render-time data (KV values, prior-step stdout) is normally still empty before anything has actually run, so dry-run output shows the raw, unrendered body with a `templated=true` note instead of risking a spurious render error.
- CUE **triple-quoted strings** (`"""..."""`) avoid having to escape embedded `"` inside template function calls like `kvGet "key"` — see the example above and [`examples/recipe/template_kv.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/template_kv.cue) for the escaping alternative.

## Related

- [Web UI — CUE recipes](./web-ui.md#cue-recipes)
- [Getting started — CUE recipes (short)](./index.md)
- [CLI: honey cue-exec](./cli/honey_cue-exec.md)
- [Kubernetes step examples](https://github.com/shareed2k/honey/tree/main/examples/recipe) — `k8s_*.cue` files
