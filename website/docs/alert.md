---
id: alert
title: Alert Investigation
---

`honey alert` connects Prometheus Alertmanager alerts to honey hosts. It has two subcommands:

| Subcommand | Purpose |
|------------|---------|
| `investigate` | Resolve a firing alert to a host and open the TUI pre-filtered to it |
| `serve` | Start an Alertmanager webhook receiver that auto-investigates alerts and sends notifications |

---

## `honey alert investigate`

Given alert labels (from `--label` flags or an Alertmanager JSON payload via `--stdin`), honey:

1. Matches labels against `alert_mappings` in your config.
2. Evaluates the mapping's `host_query` Go template against the alert labels to produce a search query.
3. Falls back to the `cluster`, `job`, `instance`, `hostname`, or `environment` label if no mapping matches.
4. Runs the host search and opens the TUI pre-filtered to the resolved host.

A red `[ALERT]` banner with alert name, severity, environment, and cluster is shown at the top of the TUI.

If the matched mapping has a `recipe` or `command` field, the suggested invocation is printed to stderr before the TUI opens.

### Usage

```bash
# From --label flags
honey alert investigate \
  --label alertname=PostgreSQLReplicationLag \
  --label cluster=postgres-main \
  --label severity=critical

# From an Alertmanager webhook payload piped via stdin
echo '{"alerts":[{"labels":{"alertname":"DiskPressure","cluster":"k8s-prod"},"status":"firing"}],"version":"4"}' \
  | honey alert investigate --stdin
```

All `honey search` flags work (`--config`, `--provider`, `--backends`, etc.).

### Config: `alert_mappings`

Add mappings to your honey YAML to control how alerts resolve to hosts:

```yaml
alert_mappings:
  - match_labels:
      alertname: PostgreSQLReplicationLag
      cluster: postgres-main
    host_query: "{{.cluster}}"       # Go template; evaluated against alert labels
    recipe: ./recipes/pg-replication.cue
    command: "psql -c 'SELECT * FROM pg_stat_replication;'"
    notify:
      subject: "Replication lag on {{.cluster}}"
      slack:
        webhook_url: https://hooks.slack.com/services/...

  - match_labels:
      alertname: DiskPressure
    host_query: "{{.instance}}"
    command: "df -h && du -sh /var/log/*"
```

**`match_labels`** — all listed labels must be present with these exact values in the firing alert.

**`host_query`** — Go template rendered against the full alert label map. The result is used as a
`honey search` name-substring filter. Available template keys are the alert's label names
(`{{.alertname}}`, `{{.cluster}}`, `{{.instance}}`, etc.).

**`recipe`** / **`command`** — printed as a suggested next step on stderr (not executed by
`investigate`; see `alert serve` for automatic execution).

**`notify`** — optional notification sent when the mapping fires during `alert serve`.

---

## `honey alert serve`

Starts an HTTP server that:
- Receives Alertmanager webhook payloads at `POST /webhook/alert`.
- Deduplicates alerts using a fingerprint LRU cache (prevents alert storms).
- Resolves each firing alert to a host via `alert_mappings`.
- Optionally runs the mapping's `command` over SSH (`auto_investigate: true`).
- Sends results to Slack, HTTP webhook, or Telegram.

### Usage

```bash
honey alert serve --port 9095 --token my-secret-token
```

Or configure everything in YAML and just run:

```bash
honey alert serve
```

### Alertmanager configuration

Point an Alertmanager receiver at the honey webhook:

```yaml
# alertmanager.yml
receivers:
  - name: honey
    webhook_configs:
      - url: http://honey-host:9095/webhook/alert
        http_config:
          bearer_token: "my-secret-token"

route:
  receiver: honey
  group_by: [alertname, cluster]
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 1h
```

### Config: `alert_webhook`

```yaml
alert_webhook:
  enabled: true
  port: 9095
  token: "my-secret-token"       # bearer token Alertmanager sends
  auto_investigate: true         # run mapping.command via SSH on matching hosts
  dedup_window: "1h"             # suppress duplicate alert fingerprints within this window
  dedup_capacity: 1000           # LRU cache size for dedup fingerprints
```

**Flag overrides:** `--port` and `--token` override YAML values for the current run.

### Notifications

Notifications are sent per `alert_mappings` entry that matches. Configure under `notify:` on each mapping.

#### Slack

```yaml
alert_mappings:
  - match_labels:
      alertname: DiskPressure
    host_query: "{{.instance}}"
    command: "df -h"
    notify:
      subject: "Disk pressure on {{.instance}}"
      slack:
        webhook_url: https://hooks.slack.com/services/T.../B.../xxx
```

Honey posts a markdown-formatted message to the Slack incoming webhook with the alert name,
matched host, investigation output (when `auto_investigate: true`), and a UTC timestamp.

#### HTTP webhook

```yaml
    notify:
      subject: "Alert: {{.alertname}}"
      http:
        url: https://my-incident-system.internal/alerts
        headers:
          X-Api-Key: "secret"
```

Honey POSTs `{"subject": "...", "body": "..."}` JSON to the URL.

#### Telegram

```yaml
    notify:
      telegram:
        bot_token: "1234567890:AAF..."
        chat_ids: ["-1001234567890"]
```

### Full example

```yaml
# honey.yaml

alert_mappings:
  - match_labels:
      alertname: PostgreSQLReplicationLag
      env: production
    host_query: "{{.cluster}}"
    command: "psql -U postgres -c 'SELECT * FROM pg_stat_replication;'"
    notify:
      subject: "PG replication lag — {{.cluster}}"
      slack:
        webhook_url: https://hooks.slack.com/services/...

  - match_labels:
      alertname: KubePodCrashLooping
    host_query: "{{.cluster}}"
    notify:
      subject: "CrashLoop: {{.pod}} in {{.namespace}}"
      slack:
        webhook_url: https://hooks.slack.com/services/...

alert_webhook:
  enabled: true
  port: 9095
  token: "changeme"
  auto_investigate: true
  dedup_window: "30m"
  dedup_capacity: 500
```

```bash
honey alert serve
```

---

## See also

- [Anomaly detection](./anomaly-detection.md) — `honey logs --alert` for streaming log anomaly notifications
- [CUE recipes](./cue-recipes.md) — run the suggested recipe after investigating
