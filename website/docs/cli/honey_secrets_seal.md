---
id: honey_secrets_seal
title: honey secrets seal
---

## honey secrets seal

Encrypt plaintext to a secure:v1 ref

### Synopsis

Reads plaintext from the argument, --file, or stdin, unwraps the stack data key from
honey config (defaults.secretsprovider + defaults.encryptedkey), and prints secure:v1:…
to stdout. Use --cue-key to emit a CUE secrets map entry.

```
honey secrets seal [plaintext] [flags]
```

### Options

```
      --cue-key string         Print CUE snippet NAME: "secure:v1:…" for secrets maps
      --data-key-file string   Test/dev: 32-byte raw AES stack key file (skips config unwrap)
      --data-key-hex string    Test/dev: 64 hex chars for 32-byte stack key (skips config unwrap)
  -f, --file string            Read input from file instead of arg/stdin
  -h, --help                   help for seal
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

* [honey secrets](honey_secrets.md)	 - Encrypt and decrypt recipe secure:v1 secret refs

