---
id: honey_logs
title: honey logs
---

## honey logs

Aggregate logs across matching hosts, pods, and containers

```
honey logs &lt;target&gt; [source] [flags]
```

### Options

```
      --alert                            Send anomaly notifications via HONEY_NOTIFY_* env vars (auto-enables --anomaly)
      --alert-suppress duration          Suppress repeated alerts for the same source+reason pair for this duration (0=no dedup) (default 5m0s)
      --anomaly                          Enable embedded anomaly detection for log lines
      --anomaly-context int              Number of recent lines sent as context to the LLM (0 = single-line mode) (default 5)
      --anomaly-endpoint string          OpenAI-compatible API base URL for LLM anomaly scoring (Ollama: http://localhost:11434/v1, LM Studio: http://localhost:1234/v1)
      --anomaly-feedback-file string     Append scored log lines as JSONL to this file for review and threshold calibration
      --anomaly-filter-threshold float   Skip LLM when fast detector score is below this value (0=disabled, 0.40=recommended for CoLA-style two-tier detection)
      --anomaly-freq-ratio float         Short/long rate ratio above which a log template is flagged as a frequency spike (default 5)
      --anomaly-freq-window int          Short window size for rate-ratio burst detection (0=disabled) (default 100)
      --anomaly-llm-model string         Model name for --anomaly-endpoint. Smaller models (3-7B) typically match or beat larger ones for binary log anomaly classification (default "llama3")
      --anomaly-model string             Path to local ONNX model file (used by embedded detector)
      --anomaly-only                     Only show lines that exceed anomaly threshold
      --anomaly-preprocessor string      Name of preprocessor to run before anomaly detection (e.g. lshd)
      --anomaly-selftest                 Validate anomaly model/tokenizer/runtime and run a local score smoke test
      --anomaly-strict                   Fail startup if anomaly detector cannot initialize
      --anomaly-threshold float          Anomaly score threshold between 0 and 1 (default 0.9)
      --anomaly-tokenizer string         Path to DistilBERT vocab.txt tokenizer file
      --anomaly-window int               Sliding window size for anomaly scoring (default 32)
      --aws-profile string               AWS shared config profile
      --aws-region string                AWS region (default: from profile/env)
      --backends string                  Comma-separated backend names (YAML backends.*.name); only those entries run
      --cmd string                       Custom remote log command for executor-backed records
      --consul-addr string               Consul HTTP address (host:port, default CONSUL_HTTP_ADDR)
      --consul-datacenter string         Consul datacenter
      --consul-token string              Consul ACL token (or CONSUL_HTTP_TOKEN)
      --container string                 Kubernetes container name for multi-container pods
      --docker-all                       Include stopped containers in docker search
      --docker-host string               Docker host (unix://, tcp://, ssh://; default: DOCKER_HOST / local socket)
      --docker-mode string               Docker search mode: containers, swarm, or both (default "containers")
      --docker-platform string           Remote Docker host OS: linux or windows (default "linux")
      --docker-socket string             Remote Docker socket (default /var/run/docker.sock on linux)
      --docker-via-local string          Docker via Honey SSH: backends.local name
      --docker-via-ssh-host string       Docker via Honey SSH: explicit host
      --file string                      Remote log file or glob to tail
      --filter stringArray               Post-discovery filter (repeatable: group:web, var:service=nginx)
  -f, --follow                           Follow logs
      --gcp-project string               GCP project (or GOOGLE_CLOUD_PROJECT / GCP_PROJECT)
      --gcp-zone string                  Limit GCP to a single zone (default: all zones)
  -g, --grep string                      Filter logs by case-insensitive regex or substring
  -h, --help                             help for logs
      --highlight                        Highlight error-like keywords in logs (default true)
      --k8s-debug-image string           Container image used for ephemeral debug containers (default: alpine:3.23)
      --k8s-mode string                  Kubernetes search mode: nodes or pods (default "nodes")
      --kube-context string              Kubernetes context override
      --kubeconfig string                Path to kubeconfig file
  -l, --label strings                    Additional host labels to show in prefix (comma-separated)
      --max-concurrency int              Maximum concurrent log streams (default 8)
      --name string                      Substring filter on instance/node/pod name (case-insensitive)
      --name-regex string                Regex filter on name (overrides --name substring)
  -o, --output-file string               Write combined log stream to this local file
      --provider string                  Comma-separated: gcp,aws,k8s,consul,proxmox,truenas,docker,local (default: all)
      --proxmox-insecure                 Skip TLS verification for Proxmox
      --proxmox-password string          Proxmox password
      --proxmox-token-id string          Proxmox token ID (e.g. root@pam!token)
      --proxmox-token-secret string      Proxmox token secret
      --proxmox-url string               Proxmox API URL (e.g. https://10.0.0.1:8006/api2/json)
      --proxmox-user string              Proxmox user (e.g. root@pam)
      --run-as string                    Run executor-backed log command as this remote user via sudo -n
      --since duration                   Only show logs newer than duration ago (e.g. 10m, 1h)
      --ssh-user string                  Default SSH user for connect actions (defaults to config or OS user)
      --tail int                         Number of lines to show from the end (default 100)
      --timestamps                       Include provider timestamps when supported
      --truenas-api-key string           TrueNAS API key (or TRUENAS_API_KEY)
      --truenas-insecure                 Skip TLS verification for TrueNAS
      --truenas-url string               TrueNAS SCALE URL (https://host or wss://host/api/current)
      --truenas-user string              TrueNAS API key username (default root)
      --tui                              Use interactive log viewer
      --unit string                      Systemd unit for SSH-like records
```

### Options inherited from parent commands

```
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --config string        Path to honey YAML (optional; also HONEY_CONFIG or default paths)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
      --no-cache             Bypass read/write cache (host discovery)
      --record-dir string    Session recording directory for search (TUI), web, and cue-exec; overrides defaults.record_dir; default &lt;directory of config.yaml&gt;/records
      --refresh              Ignore cached entries and refresh (host discovery)
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds

