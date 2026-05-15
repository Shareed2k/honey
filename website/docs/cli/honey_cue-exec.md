---
id: honey_cue-exec
title: honey cue-exec
---

## honey cue-exec

Resolve a CUE recipe against search results and optionally run steps over SSH

### Synopsis

Loads a .cue recipe (see examples/recipe), runs the same host search as honey search
(share all search flags), resolves each step's host field using search results:
literal IP, exact name match, host "*" for all rows with an IP, or host "re:PATTERN"
for a Go regexp (RE2) matched against each row's name (only rows with PrimaryIP).

Each step is exactly one of: shell command, put (upload), get (download),
script (upload a local file then run it with sh on the same SSH connection),
agent_transfer (A→cloud→B using the transfer agent; requires --config when using cloud_backend_ref),
or **ai** (terminal summarizer: runs locally in honey after all prior steps finish; `host` must be `"_"`; one non-streaming OpenAI-compatible chat request; requires `OPENAI_API_KEY` when executing).
Relative local paths are resolved against the recipe file's directory.

Then either prints a plan (--execute=false, default) or runs each step (--execute).

Optional positional name is forwarded like search: one extra argument sets the
name substring filter when --name / --name-regex are not set.

Use recipe.defaults.run_as or per-step run_as for command and script steps
(sudo -n on the remote run only); put/get SFTP uses the SSH login user.

Optional recipe.defaults.env and per-step env (map of NAME to value) set
export assignments before the shell command or sh &lt;script&gt; on the remote;
step keys override defaults. Not allowed on put/get/ai steps.

**Per-step hooks (command and script only):** a step may include `hooks.on_success` and/or `hooks.on_failure`.
Each hook runs **once per target host** immediately after that host’s main step result is streamed. Fields:

- **`where`:** `"local"` runs `sh -c` on the machine executing `honey cue-exec` (with a **60s** timeout and `Dir` set to the recipe directory). **`where: "remote"`** runs a follow-up shell command over SSH to the same host (same concurrency pool as the main step).
- **`command`:** required for hooks in this release (shell string). Remote hooks use the same export/wrap semantics as the main step.
- **`run_as`:** optional on **remote** hooks only (sudo wrapper like command steps). **`run_as` is rejected for `where: "local"`** to avoid confusion.
- **`env`:** optional map; merged after step env (hook keys override step keys), then CLI `-e` overrides on duplicate keys for **remote** hooks. Local hooks inherit the process environment, then apply hook `env`, then set injected `HONEY_*` variables (including `HONEY_HOST_NAME`, `HONEY_HOST_PRIMARY_IP`, `HONEY_HOST_PROVIDER`, `HONEY_HOST_ZONE`, `HONEY_HOOK_STEP`, `HONEY_HOOK_PHASE`, `HONEY_HOST_STEP_SUCCESS`, `HONEY_HOST_EXIT_CODE`, `HONEY_HOST_ERR_MSG`, `HONEY_RECIPE_NAME`).
- **`notify`:** optional; same shape as step-level `notify`. Hook failures **do not** change the original step success flag; failures are logged as warnings. Hook notify send errors are logged only.

With **`--execute`**, hook **stdout/stderr** is captured and printed after each host’s main output as `hook (on_success):` / `hook (on_failure):` (JSON and recordings use the same `HostExecResult` fields **`HookPhase`** and **`HookOutput`**). In the TUI, open a host row (**enter**) to see the same under **Hook**.

**Security — `where: "local"`:** local hooks run **arbitrary shell on the operator machine** (laptop, CI runner, etc.). Only use hooks from **trusted** recipe sources; consider organizational allowlists or policy gates outside honey.

**Optional `kv_tunnel` (command and script only):** set `kv_tunnel: true` on a step or under `recipe.defaults` to enable a scratch key/value HTTP API on the remote (`HONEY_KV_URL`, `HONEY_KV_TOKEN`).

**SSH targets:** For the whole `cue-exec` run, honey keeps **one** in-memory **`stepkv`** session on the operator (TTL via [jellydator/ttlcache](https://github.com/jellydator/ttlcache)) and attaches an **SSH remote forward** per pooled host connection so every SSH target with `kv_tunnel` shares the **same** key namespace across steps and hosts. **Client pooling stays on** for those steps. Parallel hosts in one step can **race** on the same keys—treat that as normal. **Security:** keys are visible across all SSH hosts in that run.

**Kubernetes pod targets (ephemeral debug exec):** On **`cue-exec`** with `kv_tunnel`, honey keeps the **same** operator **`stepkv`** session as for SSH and starts **one long-lived `kubectl exec`** per pod that runs a small **Python 3** multiplexer on loopback. Your step commands still use normal short-lived execs; they call **`curl`** (or similar) to `HONEY_KV_URL` on the pod, and traffic is tunneled over the multiplexed exec stream to operator `stepkv` (same key namespace as SSH in that run). Outside `cue-exec`, each remote invocation uses a **short-lived** in-pod Python `stepkv`-compatible server per exec (separate from operator `stepkv`). The default **`--k8s-debug-image`** (`alpine`) often has **no `python3`**; set **`recipe.defaults.k8s_debug_image`** (CLI `--k8s-debug-image`) to an image that includes Python 3 (for example **`nicolaka/netshoot:latest`**) whenever `kv_tunnel` targets pods. Use **`Authorization: Bearer $HONEY_KV_TOKEN`** or header **`X-Honey-Kv-Token`** on requests. API: **`PUT /v1/kv/{key}`** (body = value), **`GET /v1/kv/{key}`**, **`DELETE /v1/kv/{key}`**, **`GET /v1/kv/__health`**. Data is **ephemeral** (operator memory for `cue-exec`; lost when the session ends or the debug pod is deleted). Treat tokens as sensitive; anyone who can run commands in that remote context can call the KV URL on that host’s loopback.

**Other `honey` SSH entry points** (outside `cue-exec`) still use **per-run** operator `stepkv` and do not share the recipe-wide store.

**Example (remote shell, `curl`):** enable `kv_tunnel` on the step, then call the HTTP API from the same `command` / script (values are plain text; **keys must not contain `/`** — they are a single path segment. **`HONEY_HOST_NAME`** on Kubernetes pod rows often looks like `namespace/pod`; sanitize before using it in a key, e.g. `printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__'`).

```bash
# Health (optional)
curl -fsS -o /dev/null -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/__health"

# Write key "demo" (body is the value)
curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" \
  -H 'Content-Type: text/plain; charset=utf-8' \
  --data-binary "hello-from-$(hostname)" \
  "${HONEY_KV_URL}/v1/kv/demo"

# Read back
curl -fsS -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/demo"

# Delete
curl -fsS -X DELETE -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/demo"
```

Example recipes: **`examples/recipe/kv_tunnel_example.cue`** (single step: health, PUT, GET) and **`examples/recipe/kv_tunnel_multistep_example.cue`** (three steps; **SSH** and **`cue-exec` on Kubernetes** both keep KV across steps for the run). **`X-Honey-Kv-Token: $HONEY_KV_TOKEN`** works the same if you prefer that header over `Authorization`.

Repeat -e/--env KEY=value to set remote variables from the CLI; they override
recipe env on duplicate keys (command and script steps only).

With global --record-dir or defaults.record_dir in config, writes one batch .hrec.jsonl per
invocation when recording is enabled: explicit --record-dir, or record_dir set in honey YAML
(built-in default records/ alone does not enable cue-exec batch logs). Dry-run records the plan text; --execute records each step result.

**AI step credentials and defaults:** use `OPENAI_API_KEY` (required to execute an `ai` step), optional `OPENAI_BASE_URL` and `OPENAI_MODEL`. Optional honey YAML `defaults.ai_system_prompt` sets the default system message; optional recipe `ai.system_prompt` overrides it for that file only.

**Optional step notify** ([nikoksr/notify](https://github.com/nikoksr/notify)): any step may include a `notify` block. If the block is present (including `notify: {}`), notify is **enabled** for that step after the step succeeds.

- **`notify_subject`:** overrides the subject line; otherwise defaults are `honey: <recipe.name> AI summary` for an `ai` step and `honey: <recipe.name> step <N> (<kind>)` for other kinds.
- **`message`:** if set (non-empty), that string is sent as the notify **body** instead of the default (AI: model text; other steps: formatted host results). The **ai** step’s printed output in the CLI is still the model text; `message` only changes what notifiers receive.
- **`services`:** optional allowlist of backends. If **omitted**, every backend with env config is used (same as before). If **present** (including `services: {}`), only listed services run: `http: {}` (default JSON POST URLs from `HONEY_NOTIFY_HTTP_URL`), `slack: { channel_id?: string }` (incoming webhook from `HONEY_NOTIFY_SLACK_WEBHOOK_URL`; optional `channel_id` adds Slack’s `channel` field to the webhook JSON when supported), `telegram: {}` (bot + chats from env). Omitted keys are off for that step.

Secrets stay in the environment, not in CUE.

| Variable | Purpose |
|----------|---------|
| `HONEY_NOTIFY_HTTP_URL` | Comma-separated POST URLs; JSON body `{"subject","message"}` (default notify HTTP service). |
| `HONEY_NOTIFY_SLACK_WEBHOOK_URL` | Single Slack incoming webhook URL; JSON `{"text": ...}` (and optional `"channel"` from `notify.services.slack.channel_id`). |
| `HONEY_NOTIFY_TELEGRAM_BOT_TOKEN` | Telegram Bot API token (with chat IDs below). |
| `HONEY_NOTIFY_TELEGRAM_CHAT_IDS` | Comma-separated chat IDs (int64), e.g. `-1001234567890`. |

If notify is enabled but no matching env configuration exists (or `services` selects nothing usable), the **ai** step still succeeds and appends a short hint; for other steps honey logs a warning. Notify **send** errors on **ai** are appended to the model output; on other steps they are logged as warnings. They never fail the recipe by themselves. Pulling in `notify` increases module dependency size; use only in environments where that is acceptable.

```
honey cue-exec &lt;recipe.cue&gt; [name] [flags]
```

Host discovery cache (`--cache-ttl`, `--cache-dir`, `--no-cache`, `--refresh`) uses the same **global** flags as `honey search` (see **Global Flags** in `honey cue-exec --help`). Default cache TTL is **10 minutes** unless overridden by `defaults.cache_ttl` in honey YAML.

### Options

```
      --aws-profile string            AWS shared config profile
      --aws-region string             AWS region (default: from profile/env)
      --backends string               Comma-separated backend names (YAML backends.*.name); only those entries run
      --config string                 Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)
      --consul-addr string            Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)
      --consul-datacenter string      Consul datacenter
      --consul-token string           Consul ACL token (or CONSUL_HTTP_TOKEN)
  -e, --env stringArray               Remote env for command/script (repeat: -e KEY=value); overrides recipe env on duplicate keys
      --execute                       Run steps over SSH/SFTP (default: dry-run, print resolved plan only)
      --gcp-project string            GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)
      --gcp-zone string               Limit GCP to a single zone (default: all zones)
  -h, --help                          help for cue-exec
      --json                          Print results as JSON (same as --output=json)
      --k8s-debug-image string        Container image used for ephemeral debug containers (default: alpine:3.23)
      --k8s-mode string               Kubernetes search mode: nodes or pods (default "nodes")
      --kube-context string           Kubernetes context override
      --kubeconfig string             Path to kubeconfig file
      --name string                   Substring filter on instance/node/pod name (case-insensitive)
      --name-regex string             Regex filter on name (overrides --name substring)
      --no-ui                         Skip interactive UI (same as --output=json)
  -o, --output string                 Output format: tui, table, json (default "tui")
      --provider string               Comma-separated: gcp,aws,k8s,consul,proxmox (default: all)
      --proxmox-insecure              Skip TLS verification for Proxmox
      --proxmox-password string       Proxmox password
      --proxmox-token-id string       Proxmox token ID (e.g. root@pam!token)
      --proxmox-token-secret string   Proxmox token secret
      --proxmox-url string            Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)
      --proxmox-user string           Proxmox user (e.g. root@pam)
      --ssh-user string               Default SSH user for connect actions (default "shareed2k")
```

### Global flags

```
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
      --no-cache             Bypass read/write cache (host discovery)
      --record-dir string    Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
      --refresh              Ignore cached entries and refresh (host discovery)
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

