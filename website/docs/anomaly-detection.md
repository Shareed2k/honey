---
id: anomaly-detection
title: Anomaly Detection
---

# Anomaly Detection & Operational Intelligence

`honey` includes a state-of-the-art log anomaly detection and operational intelligence pipeline. Inspired by top-tier VLDB 2025 research (**CoLA** and **LLMLog**), our architecture combines ultra-fast heuristic and neural pre-screeners with Large Language Models (LLMs) to deliver high-timeliness, context-aware anomaly detection at scale.

```
                  ┌────────────────────────────────┐
                  │      Raw Log Stream Tail       │
                  └───────────────┬────────────────┘
                                  │
                                  ▼
                  ┌────────────────────────────────┐
                  │   LogLSHD Template Preprocessor │  <--- Collapse Parameter Noise
                  └───────────────┬────────────────┘
                                  │
                                  ▼
                  ┌────────────────────────────────┐
                  │     Two-Tier CoLA Filter       │  <--- Skip LLM if obvious (e.g. < 0.40)
                  └───────────────┬────────────────┘
                                  │
                                  ▼
                  ┌────────────────────────────────┐
                  │  LLMLog Adaptive Selector     │  <--- Dynamic Few-Shot (Postgres/Nginx/Node)
                  └───────────────┬────────────────┘
                                  │
                                  ▼
                  ┌────────────────────────────────┐
                  │       Local/Cloud LLM          │  <--- Deep Semantic Classification & RCA
                  └────────────────────────────────┘
```

---

## 🚀 Intelligent Preprocessing: LogLSHD

Dealing with heterogeneous, multi-source industrial logs (PostgreSQL, Nginx, Node.js) presents high lexical variance. Traditional regular expressions (RE) are rigid and fail to handle changing formats.

`honey` integrates **LogLSHD (Locality-Sensitive Hashing with Sequence-Alignment Clustering)** to convert raw, unstructured logs into clean templates in real-time (less than 1ms latency).

### Why use LogLSHD?
*   **Parameter Stripping**: It automatically collapses variable segments (IPs, timestamps, UUIDs, hex literals) into a single, clean template (e.g. `interface <*> is flapping`).
*   **Massive Caching Performance**: Stripping parameters enables our internal **LRU Cache** (up to 10,000 entries) to hit at a **95%+ rate** for high-volume logs, bypassing the slow LLM completely and boosting throughput **10×**.

### How to enable it:
Add the `--anomaly-preprocessor` flag:
```bash
honey logs myapp --anomaly --anomaly-preprocessor lshd --anomaly-only
```

---

## 🔬 Embedded ONNX Classifier (no LLM required)

For fully offline or air-gapped scoring, `honey` can run a local DistilBERT-based classifier via ONNX Runtime instead of (or alongside) an LLM.

```bash
honey logs myapp --anomaly \
  --anomaly-model ./model.onnx \
  --anomaly-tokenizer ./vocab.txt \
  --anomaly-window 32
```

| Flag | Default | Purpose |
|------|---------|---------|
| `--anomaly-model` | _(none)_ | Path to the local ONNX model file — enables the embedded detector |
| `--anomaly-tokenizer` | _(none)_ | Path to the DistilBERT `vocab.txt` tokenizer |
| `--anomaly-window` | `32` | Sliding window size for anomaly scoring |
| `--anomaly-strict` | `false` | Fail startup if the detector can't initialize (instead of silently disabling it) |
| `--anomaly-selftest` | `false` | Validate model/tokenizer/runtime and run a local smoke test (requires `--anomaly`) |

Setting **both** `--anomaly-model` and `--anomaly-endpoint` (LLM) runs **ensemble mode** — every log line is scored by both detectors in parallel; throughput is then bounded by the LLM's response time (~1–5s/line), not the ONNX model.

## 📈 Frequency-Spike Burst Detection

Independent of template/LLM scoring, `honey` tracks how often each log template recurs and flags a **burst** when the short-window rate spikes relative to the long-window baseline:

```bash
honey logs myapp --anomaly --anomaly-freq-window 100 --anomaly-freq-ratio 5.0
```

| Flag | Default | Purpose |
|------|---------|---------|
| `--anomaly-freq-window` | `100` | Short window size for rate-ratio comparison (`0` disables) |
| `--anomaly-freq-ratio` | `5.0` | Short/long rate ratio above which a template is flagged as a frequency spike |

## 🔔 Alerting on anomalies

Detected anomalies can trigger the same [alert](./alert.md) notification path used by the Alertmanager webhook receiver:

```yaml
defaults:
  logs:
    alert_enabled: true
    alert_suppress_duration: "5m"   # suppress repeat alerts for the same template
```

---

## 🧠 Dynamic Few-Shot Selection: LLMLog (VLDB 2025)

Instead of using static, generic prompts, `honey` implements **LLMLog's Adaptive Demonstration Selection** (Algorithm 3) to guide the LLM with contextually relevant, vendor-specific few-shot examples.

### How it works:
1.  **Semantic Keyword Coverage**: When a new log line arrives, `honey` runs the greedy token-cover algorithm to scan its local seed pool.
2.  **Context Assembly**: It selects the 2 best examples matching the log source's vocabulary (e.g., if you are tailing a PostgreSQL log, the LLM will receive historical PostgreSQL-specific normal and anomalous query demonstrations).
3.  **Near-100% Accuracy**: This dynamic guiding prevents LLM hallucinations, completely eliminates false positives, and aligns the model perfectly with system semantics.

---

## 🔌 Integrating Local Feedback & Training

`honey` features a closed feedback loop. You can correct or refine LLM classifications directly, and `honey` will instantly load them to retrain your model.

### 1. Web UI Logs Feedback Loop
Navigate to the **Logs Feedback** tab in the Web UI:
*   **Review Classifications**: View a complete table of logged entries with their timestamp, source, raw line, score, and reason.
*   **Direct Toggles**: Flip the **Status Switch** (`Anomaly` vs `Normal`) or fine-tune the decimal score directly.
*   **Write Reasons**: Type in a custom semantic explanation to teach the LLM why this line is benign or critical.
*   **Save Changes**: Saves directly back to your configured feedback file.

### 2. 💡 AI Suggestions
Unsure if a specific log line represents a security threat or a benign error?
*   Click the **AI Suggest** (Bulb 💡) button in the actions column.
*   `honey` queries your configured LLM, which analyzes the log line and **automatically updates the toggle, score, and writes a detailed semantic explanation** for you!

### 3. CLI Learning
To launch `honey` and load your accumulated feedback definitions:
```bash
honey logs myapp --anomaly-feedback-file ~/.config/honey/feedback.jsonl
```
*At startup, your manual corrections are prepended to the seed pool and take immediate precedence during LLMLog selection.*

---

## 🛠️ Root Cause Analysis (RCAgent & COCA)

When `honey` flags a critical anomaly, you don't have to guess why it failed.

*   Click the **RCA** button hovering over any anomalous row.
*   The backend automatically extracts the target anomaly along with its **10 preceding context lines** as a sliding sequence window.
*   An SRE agent analyses the sequence and outputs a beautifully formatted Markdown report containing:
    1.  **Root Cause Diagnosis** (e.g. database password expired).
    2.  **Downstream Impact Assessment** (e.g. auth failures, connection drops).
    3.  **Concrete Actionable Remediation** (e.g. the exact SQL commands to run).

---

## 📊 Executive Summarization (logSage)

Tailing millions of logs can cause cognitive overload. Click **Summarize** in the web console:

*   **Template Compression**: The frontend condenses all loaded console lines into their active LSHD templates and trend statistics.
*   **Operational Health Digest**: The LLM synthesizes this into an executive, 200-word digest summarizing system health, alerting you to abnormal spikes and structural OOM issues in seconds.

---

## 💻 Logs CLI Usage Examples

You can run these advanced anomaly detection features directly from your terminal using the `honey logs` command.

### 1. Simple Log Tailing with LogLSHD Template Processing
Tail VM system logs or service files and group them into structured templates in real-time:
```bash
honey logs my-server /var/log/syslog --anomaly --anomaly-preprocessor lshd
```

### 2. High-Accuracy Anomaly Detection via Local LLM (Ollama)
Stream log files and score them semantically using a local Llama/Qwen model with dynamic few-shot learning and active caching:
```bash
honey logs prod-cluster /var/log/nginx/error.log \
  --anomaly-preprocessor lshd \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-llm-model qwen3 \
  --anomaly-context 0 \
  --anomaly-only
```

### 3. CoLA-Style Two-Tier Log Pre-Screening
Save up to 80% on local CPU and remote LLM tokens by screening benign lines with our fast heuristic model first, calling the LLM only for suspicious lines (score $\ge 0.40$):
```bash
honey logs k8s-pods \
  --anomaly-preprocessor lshd \
  --anomaly-filter-threshold 0.40 \
  --anomaly-endpoint http://localhost:11434/v1 \
  --anomaly-only
```

### 4. Running with Persistent Feedback Training
Stream logs and continuously read previous manual/AI corrections so that your local LLM continues to get smarter with every log session:
```bash
honey logs prod-db postgres \
  --anomaly-preprocessor lshd \
  --anomaly-feedback-file ~/.config/honey/feedback.jsonl \
  --anomaly-only
```

---

## 🏆 Production Best Practices & Reference Defaults

To run `honey`'s operational intelligence pipeline optimally in high-throughput environments, configure defaults in your `honey.yaml`:

```yaml
defaults:
  logs:
    anomaly: true
    anomaly_threshold: 0.90
    anomaly_preprocessor: "lshd"               # Enable LogLSHD preprocessor
    anomaly_feedback_file: "~/.config/honey/feedback.jsonl" # Persistent training pool
    anomaly_endpoint: "http://localhost:11434/v1" # Local Ollama endpoint
    anomaly_llm_model: "qwen3"                 # High-performance local log model
    anomaly_context_lines: 0                   # 0 context = Unlocks 95% LRU cache hits
    anomaly_filter_threshold: 0.40             # Two-Tier CoLA Mode (bypasses LLM for benign)
    anomaly_only: true                         # Output anomalies only
```
