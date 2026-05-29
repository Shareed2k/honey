---
id: anomaly-detection
title: Anomaly Detection
---

# Anomaly Detection Best Practices

honey includes a built-in log anomaly detector that can operate in three modes: heuristic (no setup required), ONNX model (embedded neural scoring), and LLM (zero-shot via Ollama or LM Studio).

---

## Modes at a Glance

| Mode | Latency | Accuracy | Setup |
|------|---------|----------|-------|
| Heuristic | < 1 ms | Low (keyword + novelty) | None |
| ONNX | 5–50 ms | Medium | Model + tokenizer file |
| LLM | 1–5 s/line | High (context-aware) | Ollama / LM Studio running |
| Ensemble (ONNX + LLM) | 1–5 s/line | Highest | Both of the above |

---

## Quick Start

### Heuristic (zero setup)

```bash
honey logs myapp --anomaly --anomaly-only
```

Flags anomalies based on keyword severity (`panic`, `fatal`, `error`, `denied`, etc.) and novelty (lines never seen before in the session score high on first occurrence).

### LLM via Ollama

```bash
# Start Ollama with a model
ollama pull llama3
ollama serve

# Stream logs with LLM scoring
honey logs myapp \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-llm-model llama3 \
  --anomaly-only
```

### LLM via LM Studio

```bash
honey logs myapp \
  --anomaly-endpoint http://localhost:1234/v1 \
  --anomaly-llm-model "lmstudio-community/Meta-Llama-3-8B-Instruct-GGUF" \
  --anomaly-only
```

### Ensemble (ONNX + LLM)

```bash
honey logs myapp \
  --anomaly \
  --anomaly-model /path/to/model.onnx \
  --anomaly-tokenizer /path/to/vocab.txt \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-only
```

The two detectors run in parallel (latency = max, not sum). Scores are averaged before threshold comparison.

---

## Flags Reference

| Flag | Default | Description |
|------|---------|-------------|
| `--anomaly` | false | Enable anomaly detection |
| `--anomaly-only` | false | Suppress normal lines; show only anomalies |
| `--anomaly-threshold` | 0.90 | Score cutoff (0.0–1.0); lines at or above this are flagged |
| `--anomaly-model` | — | Path to local ONNX model file |
| `--anomaly-tokenizer` | — | Path to DistilBERT `vocab.txt` |
| `--anomaly-window` | 32 | Heuristic sliding window for burst detection |
| `--anomaly-endpoint` | — | OpenAI-compatible API base URL |
| `--anomaly-llm-model` | llama3 | Model name sent to LLM endpoint |
| `--anomaly-context` | 5 | Recent log lines sent as context per LLM request |
| `--anomaly-filter-threshold` | 0 | CoLA two-tier mode: skip LLM when fast detector score is below this value (0=disabled, 0.40=recommended) |
| `--anomaly-strict` | false | Fail startup if detector cannot initialize |
| `--anomaly-selftest` | false | Validate detector and run a local smoke test |

---

## Threshold Calibration

**Default threshold: 0.90** — works well for LLM and ONNX modes.

- **Lower (0.70–0.85)**: Catch more anomalies, more false positives. Good for security-sensitive workloads.
- **Default (0.90)**: Balanced for general application logs.
- **Higher (0.95–0.99)**: Very precise, lower recall. Use only when false positives are costly.

**Ensemble adjustment**: Because scores are averaged, the effective range is compressed. Lower the threshold by ~0.05 in ensemble mode:

```bash
--anomaly-threshold 0.85  # instead of 0.90 in ensemble mode
```

---

## Context Window Sizing (`--anomaly-context`)

The `--anomaly-context` flag controls how many preceding normalized log lines are sent with each LLM request. This enables sequence-aware detection — e.g., recognizing a cascade of retries before a crash.

| Value | Behavior |
|-------|----------|
| `0` | Single-line mode (fastest, lowest accuracy) |
| `3–5` | Good balance of context and token usage (recommended) |
| `10+` | Richer context, higher token cost per request |

For slow log sources (< 10 lines/s), larger windows add negligible latency. For high-throughput sources, stick with 3–5.

---

## Two-Tier Detection (`--anomaly-filter-threshold`)

Inspired by the CoLA paper (VLDB 2025), this mode avoids calling the LLM for every log line. A fast detector (heuristic in LLM-only mode, ONNX in ensemble mode) runs first. The LLM is only invoked when the fast detector's score is **at or above** the filter threshold — roughly the lines it considers suspicious or uncertain.

```bash
# LLM-only with two-tier filtering: heuristic pre-screens, LLM scores suspects
honey logs prod-cluster \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-filter-threshold 0.40 \
  --anomaly-only

# ONNX + LLM with filtering: ONNX pre-screens (CoLA pattern exactly)
honey logs prod-cluster \
  --anomaly-model /path/to/model.onnx \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-filter-threshold 0.40 \
  --anomaly-only
```

**Choosing the threshold:**

| Value | Effect |
|-------|--------|
| `0` | Disabled — LLM scores every line (default) |
| `0.40` | LLM called for lines with ≥40% fast-model suspicion — recommended starting point |
| `0.60` | LLM only for lines the fast model already leans toward anomalous |
| `0.90` | LLM only as a second-opinion when fast model is near its own threshold |

In production, 0.40 typically routes 10–30% of lines to the LLM (depending on log content), giving a 3–10× throughput improvement over unfiltered LLM mode.

**When `--anomaly-filter-threshold` and `--anomaly-context` are combined**, set `--anomaly-context 0` for maximum cache hit rate (see below), or accept that context-aware scoring disables caching.

---

## LLM Result Cache

When `--anomaly-context 0` (single-line mode), identical normalized log lines are cached and never sent to the LLM twice. This is a significant win for production logs, which contain large volumes of repeated patterns — health checks, heartbeats, templated error messages.

The cache holds up to 10,000 entries. When full, it is cleared and rebuilt. Cache hits are orders of magnitude faster than LLM calls.

To maximize cache effectiveness: use `--anomaly-context 0` and `--anomaly-filter-threshold 0.40` together. The filter eliminates cheap-to-classify normal lines; the cache eliminates redundant LLM calls for repeated suspicious patterns.

---

## Production Recommendations

### 1. Use `--anomaly-only` in production

Without this flag, every log line is printed with an `[ANOM]` prefix appended to flagged lines. With it, only anomalous lines appear — dramatically reducing noise and output volume.

```bash
honey logs prod-cluster --anomaly-only --anomaly-endpoint http://ollama:11434/v1
```

### 2. Combine with `--follow` for live tailing

```bash
honey logs prod-cluster \
  --follow \
  --anomaly-only \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-context 5
```

### 3. Write anomalies to a file

```bash
honey logs prod-cluster \
  --anomaly-only \
  --anomaly-endpoint http://localhost:11434/v1 \
  -o /var/log/honey-anomalies.log
```

### 4. Use `--anomaly-strict` in critical pipelines

This causes honey to exit with an error if the detector fails to initialize, instead of silently falling back:

```bash
honey logs prod-cluster --anomaly-strict --anomaly-endpoint http://ollama:11434/v1
```

### 5. Validate your setup with selftest

Before deploying to production, confirm the detector works end-to-end:

```bash
honey logs any-target \
  --anomaly \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-selftest
```

Expected output:
```
anomaly selftest ok: detector initialized
sample="INFO startup complete" score=0.0400 anomaly=false reason=llm:routine startup message
sample="ERROR authentication failed for user root" score=0.9600 anomaly=true reason=llm:authentication failure with privileged account
```

---

## YAML Configuration

Instead of repeating flags, configure defaults in your honey config:

```yaml
defaults:
  logs:
    anomaly: true
    anomaly_threshold: 0.90
    anomaly_endpoint: "http://localhost:11434/v1"
    anomaly_llm_model: "llama3"
    anomaly_context_lines: 5
    anomaly_filter_threshold: 0.40   # two-tier CoLA mode; 0 disables
    anomaly_only: false
```

Then run simply:

```bash
honey logs myapp --anomaly-only
```

---

## Model Selection Guide

| Model | Size | Recommended for |
|-------|------|-----------------|
| `llama3` (8B) | ~4 GB | General-purpose; good accuracy, reasonable speed |
| `mistral` (7B) | ~4 GB | Faster than llama3, slightly lower accuracy |
| `phi3` (3.8B) | ~2 GB | Low-resource machines; acceptable for simple logs |
| `llama3:70b` | ~40 GB | Highest accuracy; use only with GPU |
| `codellama` | ~4 GB | Not recommended; tuned for code, not logs |

For most workloads, **llama3** is the right starting point.

---

## How Log Normalization Works

Before scoring, every log line is normalized to reduce variable noise:

| Pattern | Replaced with |
|---------|--------------|
| UUIDs | `<uuid>` |
| MAC addresses | `<mac>` |
| IPv4 addresses | `<ip>` |
| Hex literals (`0x...`) | `<hex>` |
| Email addresses | `<email>` |
| Booleans (`true`/`false`) | `<bool>` |
| 3+ digit numbers | `<num>` |

This ensures that two lines differing only in an IP address or timestamp are treated as structurally identical, improving both heuristic novelty tracking and LLM consistency.

---

## Ensemble Mode Internals

When both `--anomaly-model` and `--anomaly-endpoint` are set:

1. Both detectors score every line **in parallel** (goroutines).
2. Final score = `(onnx_score + llm_score) / 2`.
3. Latency = `max(onnx_latency, llm_latency)` — not additive.
4. The averaged score is compared against the threshold.

A console warning is printed at startup to remind you that throughput is LLM-bound:

```
warning: ensemble mode active (ONNX + LLM) — both detectors score every log line in parallel; throughput is limited by LLM response time (~1–5 s/line)
```

---

## Alerting

When anomalies are detected, honey can send notifications to Slack, Telegram, or any HTTP webhook using the `--alert` flag on `honey logs`. Alerting auto-enables `--anomaly`.

```bash
# Alert via Slack when anomalies appear in prod logs
HONEY_NOTIFY_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/... \
honey logs prod-cluster \
  --anomaly-only \
  --alert \
  --alert-suppress 5m
```

### Environment variables

Configure at least one receiver before using `--alert`:

| Variable | Description |
|----------|-------------|
| `HONEY_NOTIFY_HTTP_URL` | Comma-separated HTTP POST URL(s) for a generic JSON webhook. Honey POSTs `{"subject": "...", "message": "..."}`. |
| `HONEY_NOTIFY_SLACK_WEBHOOK_URL` | Slack incoming webhook URL. Honey posts `{"text": "<subject>\n<body>"}`. |
| `HONEY_NOTIFY_TELEGRAM_BOT_TOKEN` | Telegram bot token. |
| `HONEY_NOTIFY_TELEGRAM_CHAT_IDS` | Comma-separated Telegram chat IDs (integers). |

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--alert` | false | Send anomaly notifications via `HONEY_NOTIFY_*` env vars (auto-enables `--anomaly`) |
| `--alert-suppress` | `5m` | Suppress repeated alerts for the same source+reason pair for this duration (`0` = no deduplication) |

### How deduplication works

Each alert is fingerprinted by `(source, reason-category)`. If the same pair fires within the suppress window it is silently dropped — you get one notification per type of anomaly per host within the window, not one per matching line. Set `--alert-suppress 0` to disable deduplication and send every anomaly.

### Notification payload

| Field | Example |
|-------|---------|
| Subject | `[honey] anomaly on prod-web-1` |
| Body | Source, score, reason, UTC timestamp, and the raw log line |

### Example: Slack + anomaly-only live tail

```bash
HONEY_NOTIFY_SLACK_WEBHOOK_URL=https://hooks.slack.com/services/T.../B.../xxx \
honey logs prod-cluster \
  --follow \
  --anomaly-only \
  --alert \
  --alert-suppress 10m \
  --anomaly-endpoint http://localhost:11434/v1
```

### YAML configuration

```yaml
defaults:
  logs:
    alert_enabled: true
    alert_suppress_duration: "5m"
```

---

## Troubleshooting

**`anomaly detector disabled: ...`** (without `--anomaly-strict`): The detector failed to initialize but honey continues without it. Add `--anomaly-strict` to surface the error.

**All lines flagged as anomalous**: Threshold too low, or heuristic mode treating every novel line as anomalous. Start with `--anomaly-threshold 0.90`. In LLM mode, check that the model is actually returning valid JSON — use `--anomaly-selftest`.

**No lines flagged**: Threshold too high, or LLM consistently returning low scores. Try `--anomaly-threshold 0.70` and inspect the `[ANOM score=X]` prefix on a few lines (temporarily remove `--anomaly-only`).

**LLM latency unacceptable**: Use `--anomaly-context 0` for single-line mode, or switch to a smaller model (`phi3`). For high-throughput sources, consider ONNX-only mode.

**`llm result parse: ...`**: The model returned malformed JSON. Some models wrap responses in markdown fences — honey strips these automatically. If the issue persists, try a different model or add an explicit instruction to your model's system template.
