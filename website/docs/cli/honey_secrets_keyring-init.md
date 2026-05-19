---
id: honey_secrets_keyring-init
title: honey secrets keyring-init
---

## honey secrets keyring-init

Create a local OS keyring entry for the stack data key

### Synopsis

Generates (or imports) a 32-byte AES stack data key and stores it in the OS credential
store (macOS Keychain, Linux secret service). Prints a YAML snippet to paste into honey config
defaults.secretsprovider. Does not modify config files.

```
honey secrets keyring-init [flags]
```

### Options

```
      --data-key-file string   Import 32-byte raw stack key from file instead of generating
      --data-key-hex string    Import 64 hex chars as stack key instead of generating
      --force                  Overwrite an existing keyring entry
  -h, --help                   help for keyring-init
      --service string         Keyring service name (keyring://service/user) (default "honey")
      --user string            Keyring account name (default "stack-data-key")
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

