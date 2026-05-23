---
id: cue-recipes
title: CUE Recipes
slug: /cue-recipes
---

Honey can run multi-step playbooks defined in [CUE](https://cuelang.org/). Each step targets hosts from your current search and performs **exactly one** action: `command`, `put`, `get`, `script`, `agent_transfer`, `ai`, `plugin` (WASM — see [Plugin development](./plugins-development.md)), or `tunnel` (operator-side port forward).

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
| `"_"` | Local only — required for `ai` steps |

For **`agent_transfer`**, `host` is the **source**; `agent_transfer.dest_host` selects the destination (each must match exactly one row).

## Step kinds

| Kind | Remote? | Notes |
|------|---------|-------|
| `command` | SSH / k8s exec | Optional `env`, `secrets`, `hooks`, `kv_tunnel` |
| `script` | SSH / k8s exec | Upload `local` → `remote`, then `sh <remote>` |
| `plugin` | SSH / k8s exec | WASM custom step — [Plugin development](./plugins-development.md) |
| `tunnel` | SSH / k8s / TrueNAS | Operator-side listen (local/remote/dynamic/UDP/tun) — [Tunnel steps](#tunnel-steps) |
| `put` / `get` | SFTP or k8s tar stream | Relative `local` paths from recipe directory |
| `agent_transfer` | A→cloud→B | Needs honey config for `cloud_backend_ref` |
| `ai` | Local (operator) | `host: "_"`; needs `OPENAI_API_KEY` when executing |

Optional **`recipe.defaults`**: `run_as`, `env`, `secrets`, `kv_tunnel`, `max_parallel`, `ssh_port`, `ssh_private_key`, `k8s_debug_image`.

Remote env injection (command/script/plugin): `HONEY_HOST_NAME`, `HONEY_HOST_PRIMARY_IP`, `HONEY_HOST_PROVIDER`, `HONEY_HOST_ZONE`, `HONEY_HOST_REGION`, and `HONEY_HOST_META_*` from host metadata.

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

- **`env_from`:** map a dependency step’s **stdout** into env vars (per host). Each `env_from[].step` must appear in **`depends`**. Supported on `command`, `script`, and `plugin` only.
- **`HONEY_STEP_ID`:** set on remote env when the step has an `id` (use to namespace shared KV keys).
- **`kv_tunnel`:** one operator-side `stepkv` session for the whole run; see [KV tunnel](#recipe-kv-tunnel) below.
- **Failure / skip:** if a step fails (or all hosts hit transient SSH errors), **descendants are skipped**. If an **`ai`** step becomes unreachable, the run **aborts**.
- **Web UI:** Recipes wizard → **Graph** tab shows the DAG (`POST /api/v1/recipes/validate-content` → `graph` field).

Example: [`graph_parallel.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/graph_parallel.cue).

## Conditional steps (`when` + CEL)

Optional **`when: "<CEL expression>"`** on any step kind. The expression must evaluate to **bool**. When false, that target is **skipped** without SSH/SFTP:

| Kind | When evaluated |
|------|----------------|
| `command`, `script`, `plugin`, `put`, `get` | Per expanded target host |
| `agent_transfer` | Once on the **source** host (`dest.*` available in CEL) |
| `ai` | Once locally (`host.name == "_"`; `steps` uses aggregated prior results) |

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

## Tunnel steps

A **`tunnel:`** step opens a TCP/UDP listen address (or tun device) on the **operator** — the machine where `honey cue-exec` runs. Honey dials the recipe target (SSH, k8s port-forward API, or TrueNAS API shell) and forwards traffic to a service on **remote loopback** or inside a pod.

Use tunnels for Redis, HTTP APIs, Postgres (via the postgres plugin), SOCKS browsing through a bastion, or any protocol that fits the forward mode. The listen socket is always on the operator (`127.0.0.1:<port>` by default), not on the remote host.

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

**Local forward (default)** — reach remote loopback from the operator:

```cue
tunnel: { remote_host: "localhost", remote_port: 8080 }
```

**SOCKS5 proxy (bastion → internal HTTP)** — reach VPC-only hostnames from your laptop via a jump host. Example: [`tunnel_socks.cue`](https://github.com/shareed2k/honey/blob/main/examples/recipe/tunnel_socks.cue).

```cue
tunnel: { mode: "dynamic", bind: "127.0.0.1", local_port: 1080 }
```

```bash
honey cue-exec --execute examples/recipe/tunnel_socks.cue "bastion-*"
curl --socks5-hostname 127.0.0.1:1080 http://grafana.internal:3000/api/health
```

In Firefox, set SOCKS v5 to `127.0.0.1:1080` and enable **Proxy DNS when using SOCKS v5** so internal names resolve on the bastion.

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

## Secrets and env

- **`env`:** literal `KEY=value` maps on command/script/plugin (and remote hooks).
- **`secrets`:** values must be `secure:v1:…` refs; decrypted at execute time. See [examples/recipe README — Secrets authoring](https://github.com/shareed2k/honey/blob/main/examples/recipe/README.md#secrets-authoring).
- CLI **`-e KEY=value`** overrides recipe env on duplicate keys (command/script only).

## Hooks, notify, and AI

- **Hooks** (`on_success` / `on_failure`): command/script/plugin only; local or remote follow-up per host. See [`honey cue-exec`](./cli/honey_cue-exec.md).
- **`notify`:** optional per-step notifications after success ([nikoksr/notify](https://github.com/nikoksr/notify)).
- **`ai`:** terminal summarizer after prior steps; optional `notify` and `when` (aggregated `steps` view).

## Related

- [Web UI — CUE recipes](./web-ui.md#cue-recipes)
- [Getting started — CUE recipes (short)](./index.md#cue-recipes-experimental)
- [CLI: honey cue-exec](./cli/honey_cue-exec.md)
