# Honey Recipe Example

This directory contains an example [CUE](https://cuelang.org/) recipe that demonstrates how to automate multi-host deployments and execution using `honey cue-exec`.

## Files

- `example.cue`: The fully documented recipe that demonstrates global defaults, variable injection, using the injected `hosts` variable list for dynamic step generation, and different step kinds (`command`, `put`, `get`, `script`, `agent_transfer`, `ai`). Optional **`defaults.ssh_port`** / per-step **`ssh_port`** (1–65535) override the SSH dial port for that step with precedence **step → defaults → each host’s `meta.ssh_port` → `~/.ssh/config` / 22**. Optional **`defaults.ssh_private_key`** / per-step **`ssh_private_key`** set a private key path for SSH with precedence **step → defaults → unset (normal `~/.ssh/config` / env / `~/.ssh` keys)**; when set, honey uses **only** that key file (no `IdentityFile`, `HONEY_SSH_IDENTITY_FILES`, or default `~/.ssh` fallbacks).
- `with_ssh_key.cue`: Minimal recipe showing `ssh_private_key` on defaults and a per-step override.
- `graph_parallel.cue`: **`type: "graph"`** with per-step **`id`** and **`depends`** for parallel waves (see below).
- `graph_env_from.cue`: Graph mode **`env_from`** — map dependency step **stdout** into env (per host).
- `template_render.cue`: Graph **`template`** step — Go `text/template` locally (`host: "_"`), **`template.output`** capture + **`env_from.from_output`**.
- `template_var_expand.cue`: **`${VAR}`** expansion in **`template.data`** string values (not in the template body).
- `template_per_host.cue`: Per-host **`template`** (`host: "*"`) with host-scoped **`env_from.step`** (no `template.output` on multi-host).
- `template_kv.cue`: **`kv_tunnel`** command writes KV; local **`template`** reads **`kvGet`** / **`kvHas`**.
- `template_linear.cue`: Linear recipe with a single local template step (smoke test).
- `graph_kv_tunnel.cue`: Graph mode **`defaults.kv_tunnel`** shared across waves; namespace keys with **`HONEY_STEP_ID`** and **`HONEY_HOST_NAME`**.
- `graph_when.cue`: Graph mode **`when`** (CEL) — run a step per host only if a CEL expression is true (e.g. prior step stdout).
- `graph_when_kv.cue`: **`when`** with **`kv_get` / `kv_has`** against operator-local recipe KV (`defaults.kv_tunnel`).
- `graph_when_secrets.cue`: **`when`** reading declared **`secrets`** keys (resolved on `--execute`, redacted on dry-run).
- `agent_transfer.cue`: Example **A→cloud→B** staging transfer (transfer agent); see the file header for host-arity rules and `cloud_backend_ref` / `--config` requirements.
- `clean_filesystem.cue`: Maintenance recipe for systemd journal usage/vacuum and snap (remove disabled revisions, clear `/var/lib/snapd/cache`); read the file header for destructive journal behavior and `sudo -n` requirements.
- `high_load_processes.cue`: On Linux (GNU `ps`), prints load, `free -h`, and top processes by **CPU%** and **RSS**; uses `host: "*"` for every matched host with an IP.
- `k8s_node_pod_cpu_hint.cue`: For **Kubernetes worker** nodes over SSH: load, top PIDs, `/proc/<pid>/cgroup` snippets (pod UID hints), optional `crictl stats` / `crictl pods`, then optional **`ai`** summary; see file header for `sudo`/PATH and cgroup caveats.
- `postgres_replica_lag.cue`: Read-only Postgres triage (replication lag snapshot, long-running `pg_stat_activity` sessions over 5 minutes, postgres process snapshot); set `PG*` via `defaults.env` and pass **`PGPASSWORD` via `cue-exec -e`**; see file header.
- `kv_tunnel_multistep_example.cue`: Three **`command`** steps with **`defaults.kv_tunnel: true`** — one operator `stepkv` for the whole `cue-exec` on **SSH and Kubernetes** (pods use a long-lived exec bridge to that session). Per-host keys sanitize `HONEY_HOST_NAME` for `/` and `:`.
- `echo_plugin_demo.cue` / `echo_plugin_kv_demo.cue`: **`plugin:`** steps with the echo WASM plugin (`noop`, `host_exec`, and **`kv_ping`** via `pkg/pluginpdk` + remote `curl` for shared KV); requires `plugins.enabled` and echo installed — see `examples/plugins/echo/README.md` and `examples/plugins/README.md` (Recipe KV from Go plugins).
- `postgres_logical_replication_slots.cue`: Read-only logical replication triage (`pg_replication_slots`, `pg_publication`, `pg_replication_slot_advance` in `pg_stat_activity`, primary-only WAL distance); same `PG*` / `-e PGPASSWORD` pattern; see file header for Grafana/Wazuh and destructive follow-ups not in the recipe.
- `ai_summarize_hosts.cue`: Sample `command` steps on `host: "*"` then a final **`ai`** step (`host: "_"`); needs `OPENAI_API_KEY` for `--execute`; optional **`notify`** (`notify_subject`, `message`, `services` allowlist, `slack.channel_id`) + `HONEY_NOTIFY_*` env for [notify](https://github.com/nikoksr/notify); see file header and `honey cue-exec` docs.
- `with_env.cue` / `with_secrets.cue`: Literal **`env`** maps vs **`secrets`** maps; **`secrets` values must be `secure:v1:…` only**. Requires honey `defaults.secretsprovider` + `defaults.encryptedkey` (or a test static key); dry-run redacts; `--execute` decrypts on the operator host.
- `with_secrets_stores.cue`: Same symmetric-only model with provider URL examples (GCP/AWS KMS, Vault Transit, K8s, keyring, age).
- `assets/index.html`: A dummy file used to demonstrate the `put` (upload) step.
- `scripts/setup.sh`: A shell script used to demonstrate the `script` (upload and execute) step.
- `downloads/`: An empty folder to receive files retrieved by the `get` step.

## Secrets authoring

Recipe `secrets:` values must be `secure:v1:<nonce-b64>:<ciphertext-b64>`. Use the stack data key from honey `defaults.secretsprovider` and `defaults.encryptedkey` (see `with_secrets_stores.cue` header for provider URLs).

### Local Keychain (macOS) / secret service (Linux)

```bash
# Create stack key in OS keyring and print YAML to paste into ~/.config/honey/config.yaml
honey secrets keyring-init

# Overwrite an existing entry, or custom service/account names
honey secrets keyring-init --force
honey secrets keyring-init --service honey --user stack-data-key
```

Then seal recipe secrets:

```bash
# Encrypt plaintext → secure:v1 ref (stdout)
echo -n 'my-db-password' | honey secrets seal --config ~/.config/honey/config.yaml

# CUE map entry for pasting into a recipe
honey secrets seal --cue-key DB_PASSWORD -f ./password.txt --config ~/.config/honey/config.yaml

# Verify decrypt (plaintext goes to stdout; may appear in shell history)
honey secrets unseal 'secure:v1:…' --config ~/.config/honey/config.yaml

# Local test without cloud KMS (64 hex chars = 32-byte key)
KEY=$(openssl rand -hex 32)
honey secrets seal --data-key-hex "$KEY" 'hello'
```

## Graph mode (explicit dependencies)

By default recipes run **linearly** (steps in array order). Set **`type: "graph"`** when independent steps should run in **parallel** once their dependencies succeed:

- Each step needs a unique **`id`** (`[a-zA-Z][a-zA-Z0-9_-]*`).
- Optional **`depends: [id, ...]`** lists prerequisite steps (must form a **DAG**, no cycles).
- Honey runs steps in **waves** (all steps in a wave may run concurrently; default up to 8 steps at once).
- Optional **`max_parallel`** (1–128) on `defaults` or a step caps **host-level** SSH/SFTP/plugin concurrency for that step (default 32); it does **not** limit how many steps run in one graph wave.
- Optional **`env_from`** on a step maps env vars from a dependency step’s captured **stdout** (per host) or from a **`template.output`** capture name via **`from_output`**; each ref must appear in that step’s **`depends`** (exactly one of **`step`** or **`from_output`** per entry).
- Optional **`template`** step (local): **`host: "_"`** for a single render, or **`host: "*"`** / literal / **`re:`** for per-host renders. Block fields: **`template`** (body), **`data`**, **`output`** (capture name only, requires **`host: "_"`**). Template body uses Go **`text/template`** + slim-sprig; **`${VAR}`** in **`data`** values is expanded from captures / env (not in the template body).
- Graph mode sets **`HONEY_STEP_ID`** on remote command/script/plugin env when the step has an **`id`** (use with shared KV keys).
- **`kv_tunnel`** may be enabled on multiple graph steps (or via `defaults.kv_tunnel`); one shared stepkv session for the run — dependency waves order reads; **same-wave** steps may race (namespace keys per host/step).
- If a step **fails** (or all hosts hit transient SSH errors), **descendants are skipped**; other branches continue.
- If the recipe has an **`ai`** step and it becomes **unreachable** (a dependency failed or was skipped), the whole run **aborts**.
- Linear recipes must not use `id` / `depends` / `env_from`; graph recipes relax the rule that `ai` must be last in the `steps` array (but `ai` must not be listed in any other step’s `depends`).
- Web UI: in the recipe wizard (Step ③ Review plan), use the **Graph** tab for a read-only DAG (powered by `POST /api/v1/recipes/validate-content` → `graph` field).

See [`graph_parallel.cue`](graph_parallel.cue) for a fetch → parallel restarts → verify → ai example.

## Conditional steps (`when` + CEL)

Optional **`when: "<CEL expression>"`** on any step kind (`command`, `script`, `plugin`, `put`, `get`, `agent_transfer`, `ai`). The expression must evaluate to **bool**. When false, that host (or the whole `agent_transfer` / `ai` step) is **skipped** without SSH/SFTP.

- **`id` is required** whenever `when` is set (linear or graph) so `steps['fetch']` is stable. In linear recipes, `id` is only allowed on steps that have `when`.
- Graph: step ids referenced in `when` must appear in that step’s **`depends`**.
- If **every** target for a graph step is when-skipped, the step is treated as **skipped** and **dependents are skipped** (same as a failed branch).

### CEL variables and functions

| Name | Type | Meaning |
|------|------|---------|
| `host.name`, `host.ip`, `host.provider`, `host.zone`, `host.region` | string | Current target host |
| `host.meta` | map | Host metadata (`hosts.Record.Meta`) |
| `host.extra_ips` | list | Extra IPs |
| `dest.*` | same as `host` | Destination host (`agent_transfer` only) |
| `steps['id'].succeeded` | bool | Prior step outcome on this host |
| `steps['id'].skipped` | bool | Prior step was skipped |
| `steps['id'].stdout` | string | Captured stdout (command/script/plugin) |
| `steps['id'].exit_code` | int | Remote exit code |
| `secrets['KEY']` | string | Declared `defaults.secrets` / `step.secrets` only |
| `execute` | bool | `false` on dry-run / plan |
| `recipe_name` | string | Recipe name |
| `kv_get(key)` | string | Operator recipe KV value (`""` if missing) |
| `kv_has(key)` | bool | Whether key exists in recipe KV |

**Security:** `secrets` in CEL use the same resolver as recipe env — as sensitive as putting values in `step.secrets`. Dry-run plans never print resolved secret material. KV reads are operator-local (what prior steps wrote in the run).

## How to use

In addition to your own variables (like `APP_ENV`), `honey` automatically injects host-specific environment variables for every step you run. This means you can use `$HONEY_HOST_NAME`, `$HONEY_HOST_PRIMARY_IP`, `$HONEY_HOST_PROVIDER`, `$HONEY_HOST_ZONE`, and meta variables directly inside your shell commands and scripts (e.g. `echo $HONEY_HOST_PRIMARY_IP`).

You can run this recipe against your infrastructure by passing it to `honey cue-exec`. The CLI will first resolve your target hosts according to your search filters, and then safely resolve the steps.

By default, it runs in **dry-run** mode, outputting the plan:

```bash
# Preview what would happen on hosts matching "web-"
honey cue-exec examples/recipe/example.cue "web-"
```

When you are ready to apply the changes, append the `--execute` flag:

```bash
# Execute the recipe on matching hosts
honey cue-exec examples/recipe/example.cue "web-" --execute
```
