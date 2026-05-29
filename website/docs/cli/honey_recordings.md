---
id: honey_recordings
title: honey recordings
---

## honey recordings

Manage session recordings

### Options

```
  -h, --help   help for recordings
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
* [honey recordings export](honey_recordings_export.md)	 - Export a recording to asciinema v3 .cast format
* [honey recordings grep](honey_recordings_grep.md)	 - Search decoded session output across recordings
* [honey recordings list](honey_recordings_list.md)	 - List available recordings
* [honey recordings prune](honey_recordings_prune.md)	 - Delete recordings older than a given age
* [honey recordings replay](honey_recordings_replay.md)	 - Replay a session recording in the terminal
* [honey recordings stats](honey_recordings_stats.md)	 - Show per-recording statistics (duration, bytes, exit code)
* [honey recordings summarize](honey_recordings_summarize.md)	 - Summarize a recording using an LLM (requires OPENAI_API_KEY)

