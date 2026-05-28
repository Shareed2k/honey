---
id: honey_logs
title: honey logs
---

## honey logs

Aggregate logs across matching hosts, pods, and containers

```
honey logs <target> [source] [flags]
```

### Examples

```
  # Follow nginx logs on all web hosts
  honey logs "web-*" --unit nginx --follow --tail 200

  # Tail Kubernetes pod logs and filter for errors
  honey logs "api-*" --provider k8s --follow --grep "error|warn"

  # Stream logs to a file while watching in the terminal
  honey logs "db-*" --unit postgresql --follow -o /tmp/db.log

  # Enable anomaly detection with a local ONNX model
  honey logs "web-*" --anomaly --anomaly-model /models/distilbert.onnx \
    --anomaly-tokenizer /models/vocab.txt --anomaly-threshold 0.85 --anomaly-only

  # Validate anomaly model and run a smoke test
  honey logs "web-*" --anomaly --anomaly-selftest
```

### Options

```
      --anomaly                   Enable embedded anomaly detection for log lines
      --anomaly-model string      Path to local ONNX model file (used by embedded detector)
      --anomaly-only              Only show lines that exceed anomaly threshold
      --anomaly-selftest          Validate anomaly model/tokenizer/runtime and run a local score smoke test
      --anomaly-strict            Fail startup if anomaly detector cannot initialize
      --anomaly-threshold float   Anomaly score threshold between 0 and 1 (default 0.9)
      --anomaly-tokenizer string  Path to DistilBERT vocab.txt tokenizer file
      --anomaly-window int        Sliding window size for anomaly scoring (default 32)
      --cmd string                Custom remote log command for executor-backed records
      --container string          Kubernetes container name for multi-container pods
  -f, --follow                    Follow logs
  -g, --grep string               Filter logs by case-insensitive regex or substring
  -h, --help                      help for logs
      --highlight                 Highlight error-like keywords in logs (default true)
  -l, --label strings             Additional host labels to show in prefix (comma-separated)
      --max-concurrency int       Maximum concurrent log streams (default 8)
  -o, --output-file string        Write combined log stream to this local file
      --run-as string             Run executor-backed log command as this remote user via sudo -n
      --since duration            Only show logs newer than duration ago (e.g. 10m, 1h)
      --tail int                  Number of lines to show from the end (default 100)
      --timestamps                Include provider timestamps when supported
      --tui                       Use interactive log viewer
      --unit string               Systemd unit for SSH-like records
```

### Options inherited from parent commands

```
      --backends string      Comma-separated backend names (YAML backends.*.name); only those entries run
      --cache-dir string     Override cache directory (default: XDG_CACHE_HOME/honey)
      --cache-ttl duration   Cache time-to-live (host discovery) (default 10m0s)
      --config string        Path to honey YAML (optional; also HONEY_CONFIG or default paths in README)
      --debug-log string     Path to write debug logs (disables debug logging if empty)
      --no-cache             Bypass read/write cache (host discovery)
      --provider string      Comma-separated provider IDs to restrict search
      --record-dir string    Session recording directory; overrides defaults.record_dir
      --refresh              Ignore cached entries and refresh (host discovery)
```

### SEE ALSO

* [honey](honey.md)	 - DevOps tool to help find an instance in sea of clouds
