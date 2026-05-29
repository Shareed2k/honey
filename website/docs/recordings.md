---
id: recordings
title: Session Recordings
---

Honey records SSH, Kubernetes, and Docker terminal sessions when `--record-dir` is set. The `honey recordings` commands let you list, search, export, replay, and prune those recordings.

## Enabling recordings

Pass `--record-dir` to `honey web` or `honey search` (TUI), or set it in your config:

```yaml
defaults:
  record_dir: ~/.local/share/honey/records
```

Or override per command:

```bash
honey web --record-dir /var/log/honey/records
```

**Resolution order** (first wins):

1. `--record-dir` CLI flag
2. `defaults.record_dir` in config YAML
3. `<config-file-dir>/records`

Recordings are stored as `.hrec.jsonl` files — newline-delimited JSON events (timing, direction, data).

---

## Commands

### List recordings

```bash
honey recordings list
```

Prints one basename per line. Each basename is the session identifier used by all other subcommands.

### Replay in the terminal

```bash
honey recordings replay <basename>
```

Replays the session output in your terminal at the original speed.

### Export to asciinema

```bash
# Print to stdout
honey recordings export <basename>

# Write to file
honey recordings export <basename> -o session.cast
```

Produces an [asciinema v3 `.cast`](https://docs.asciinema.org/manual/asciicast/v3/) file you can share or upload to asciinema.org.

### Stats

```bash
# Human-readable table
honey recordings stats

# Specific sessions
honey recordings stats <basename1> <basename2>

# JSON output
honey recordings stats --json
```

Shows duration, bytes in/out, error count, and exit code for each session.

### Search across recordings

```bash
# Case-insensitive fixed-string search (default)
honey recordings grep "authentication failed"

# Regex search
honey recordings grep -E "error.*timeout" 

# Narrow to specific recordings
honey recordings grep "nginx" session1 session2

# Include stdin events (default: stdout+stderr only)
honey recordings grep --stdin "sudo"

# Show context lines around each match
honey recordings grep -B 2 -A 2 "panic"
```

Grep flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-E`, `--regex` | false | Treat pattern as a Go regular expression (RE2) |
| `--stdin` | false | Also search stdin events |
| `-B`, `--before` | 0 | Show N events before each match |
| `-A`, `--after` | 0 | Show N events after each match |

### Summarize with an LLM

```bash
# Uses OPENAI_API_KEY and defaults to gpt-4o-mini
honey recordings summarize <basename>

# Use a specific model
honey recordings summarize <basename> --model gpt-4o
```

Sends the session transcript to an OpenAI-compatible API and prints a structured summary covering: which recipe or command ran, how many hosts, per-host success/failure, exit codes, and notable output.

Requires `OPENAI_API_KEY`. Optional `OPENAI_BASE_URL` to use a local provider (e.g. LM Studio).

### Prune old recordings

```bash
# Delete recordings older than 7 days
honey recordings prune --older-than 7d

# Preview what would be deleted
honey recordings prune --older-than 30d --dry-run

# Duration can be in days (d) or standard Go duration (h, m, s)
honey recordings prune --older-than 720h
```

Prune flags:

| Flag | Required | Description |
|------|----------|-------------|
| `--older-than` | Yes | Age threshold (e.g. `7d`, `720h`) |
| `--dry-run` | No | List files that would be deleted without deleting them |

---

## Web UI

When `--record-dir` is set, the web UI (`honey web`) shows a **Recordings** section in the Apps tab. Sessions can be listed and replayed directly in the browser.

API endpoints:

- `GET /api/v1/recordings` — list available recordings
- `POST /api/v1/recordings/play` — fetch payload for browser replay

---

## CLI reference

- [`honey recordings`](./cli/honey_recordings.md)
- [`honey recordings list`](./cli/honey_recordings_list.md)
- [`honey recordings export`](./cli/honey_recordings_export.md)
- [`honey recordings replay`](./cli/honey_recordings_replay.md)
- [`honey recordings stats`](./cli/honey_recordings_stats.md)
- [`honey recordings grep`](./cli/honey_recordings_grep.md)
- [`honey recordings summarize`](./cli/honey_recordings_summarize.md)
- [`honey recordings prune`](./cli/honey_recordings_prune.md)
