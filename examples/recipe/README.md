# Honey Recipe Example

This directory contains an example [CUE](https://cuelang.org/) recipe that demonstrates how to automate multi-host deployments and execution using `honey cue-exec`.

## Files

- `example.cue`: The fully documented recipe that demonstrates global defaults, variable injection, using the injected `hosts` variable list for dynamic step generation, and different step kinds (`command`, `put`, `get`, `script`, `agent_transfer`, `ai`). Optional **`defaults.ssh_port`** / per-step **`ssh_port`** (1–65535) override the SSH dial port for that step with precedence **step → defaults → each host’s `meta.ssh_port` → `~/.ssh/config` / 22**.
- `agent_transfer.cue`: Example **A→cloud→B** staging transfer (transfer agent); see the file header for host-arity rules and `cloud_backend_ref` / `--config` requirements.
- `clean_filesystem.cue`: Maintenance recipe for systemd journal usage/vacuum and snap (remove disabled revisions, clear `/var/lib/snapd/cache`); read the file header for destructive journal behavior and `sudo -n` requirements.
- `high_load_processes.cue`: On Linux (GNU `ps`), prints load, `free -h`, and top processes by **CPU%** and **RSS**; uses `host: "*"` for every matched host with an IP.
- `k8s_node_pod_cpu_hint.cue`: For **Kubernetes worker** nodes over SSH: load, top PIDs, `/proc/<pid>/cgroup` snippets (pod UID hints), optional `crictl stats` / `crictl pods`, then optional **`ai`** summary; see file header for `sudo`/PATH and cgroup caveats.
- `postgres_replica_lag.cue`: Read-only Postgres triage (replication lag snapshot, long-running `pg_stat_activity` sessions over 5 minutes, postgres process snapshot); set `PG*` via `defaults.env` and pass **`PGPASSWORD` via `cue-exec -e`**; see file header.
- `kv_tunnel_multistep_example.cue`: Three **`command`** steps with **`defaults.kv_tunnel: true`** — one operator `stepkv` for the whole `cue-exec` on **SSH and Kubernetes** (pods use a long-lived exec bridge to that session). Per-host keys sanitize `HONEY_HOST_NAME` for `/` and `:`.
- `postgres_logical_replication_slots.cue`: Read-only logical replication triage (`pg_replication_slots`, `pg_publication`, `pg_replication_slot_advance` in `pg_stat_activity`, primary-only WAL distance); same `PG*` / `-e PGPASSWORD` pattern; see file header for Grafana/Wazuh and destructive follow-ups not in the recipe.
- `ai_summarize_hosts.cue`: Sample `command` steps on `host: "*"` then a final **`ai`** step (`host: "_"`); needs `OPENAI_API_KEY` for `--execute`; optional **`notify`** (`notify_subject`, `message`, `services` allowlist, `slack.channel_id`) + `HONEY_NOTIFY_*` env for [notify](https://github.com/nikoksr/notify); see file header and `honey cue-exec` docs.
- `assets/index.html`: A dummy file used to demonstrate the `put` (upload) step.
- `scripts/setup.sh`: A shell script used to demonstrate the `script` (upload and execute) step.
- `downloads/`: An empty folder to receive files retrieved by the `get` step.

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
