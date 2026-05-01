# CUE recipe examples

Validate any file:

```bash
honey cue-validate examples/recipe/<file>.cue
```

Dry-run (resolves hosts from the same search as `honey search`; optional positional name substring):

```bash
honey cue-exec examples/recipe/<file>.cue my-filter
```

Run over SSH:

```bash
honey cue-exec --execute examples/recipe/<file>.cue my-filter
```

| File | Shows |
|------|--------|
| [`recipe.cue`](recipe.cue) | Basic steps with literal IPs; comments for `*`, `re:`, `run_as` |
| [`all_hosts.cue`](all_hosts.cue) | `host: "*"` — same commands on every host in the search result |
| [`by_regex.cue`](by_regex.cue) | `host: "re:…"` — name subset via regexp |
| [`mixed_hosts.cue`](mixed_hosts.cue) | `*`, `re:`, and literal IP in one recipe |
| [`with_run_as.cue`](with_run_as.cue) | `defaults.run_as` and per-step `run_as` override |
| [`file_transfer.cue`](file_transfer.cue) | `put` (upload) and `get` (download); relative paths vs this folder |
| [`script_step.cue`](script_step.cue) | `script` — upload [`hello.sh`](hello.sh) then run it (one SSH connection per host) |
| [`with_env.cue`](with_env.cue) | `defaults.env` and per-step `env` for `command` / `script` |

See the main [README](../../README.md) § CUE recipes for full `host` semantics and flags.
