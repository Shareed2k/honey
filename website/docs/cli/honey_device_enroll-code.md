---
id: honey_device_enroll-code
title: honey device enroll-code
---

## honey device enroll-code

Mint a one-time device enrollment code and print a QR to scan from the app

### Synopsis

Calls the running honey server's admin API to mint a single-use enrollment
code, then prints a QR encoding the bootstrap the mobile app scans: the enroll
URL, the code, and the device CA fingerprint (for pinning).

Examples:
  honey device enroll-code --token "$HONEY_WEB_TOKEN"
  honey device enroll-code --admin-url http://localhost:8765 \
    --enroll-url https://honey.example --cn device:phone-1

```
honey device enroll-code [flags]
```

### Options

```
      --admin-url string    Base URL of the running honey server (to mint the code) (default "http://localhost:8765")
      --cn string           Device certificate CN (default device:&lt;random&gt;)
      --enroll-url string   Base URL the app uses to enroll (embedded in the QR); defaults to --admin-url
  -h, --help                help for enroll-code
      --insecure            Skip TLS verification when calling the admin URL
      --token string        Admin auth token (default $HONEY_WEB_TOKEN)
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

* [honey device](honey_device.md)	 - Manage device mTLS enrollment

