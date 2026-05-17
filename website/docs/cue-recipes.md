---
id: cue-recipes
title: CUE Recipes
slug: /cue-recipes
---

Honey can run multi-step playbooks defined in [CUE](https://cuelang.org/). Each step targets hosts from your current search and performs **exactly one** action: `command`, `put`, `get`, `script`, `agent_transfer`, `ai`, or `plugin` (WASM — see [Plugin development](./plugins-development.md)).

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

## Recipe KV tunnel

On `command`, `script`, and `plugin` steps (or `defaults.kv_tunnel`), honey starts one in-memory **stepkv** session on the operator and exposes it to remotes via `HONEY_KV_URL` and `HONEY_KV_TOKEN`.

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
