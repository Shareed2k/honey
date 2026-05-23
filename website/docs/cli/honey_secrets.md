---
id: honey_secrets
title: honey secrets
---

## honey secrets

Encrypt and decrypt recipe secure:v1 secret refs

### Synopsis

Seal plaintext into secure:v1 refs for CUE recipes, or unseal refs for verification. Uses defaults.secretsprovider and defaults.encryptedkey from honey config.

### Options

```
      --config string   Path to honey YAML (optional; also HONEY_CONFIG or default paths)
  -h, --help            help for secrets
```

### Options inherited from parent commands

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
* [honey secrets keyring-init](honey_secrets_keyring-init.md)	 - Create a local OS keyring entry for the stack data key
* [honey secrets seal](honey_secrets_seal.md)	 - Encrypt plaintext to a secure:v1 ref
* [honey secrets unseal](honey_secrets_unseal.md)	 - Decrypt a secure:v1 ref to plaintext

