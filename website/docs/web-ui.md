---
id: web-ui
title: Web UI & AI assist
---

Honey includes an embedded web server that serves a React-based UI, providing a visual interface for search, configuration, browser terminal sessions, optional session recording, file transfer, and AI-assisted help for terminals and CUE recipes.

The Web UI binds to **loopback only** (`127.0.0.1`, `localhost`, or `::1`) and uses **token-based** authentication.

## Starting the Web UI

```bash
# One-time: embed UI assets into the binary tree (CI usually does this)
make webui

go build -o honey ./cmd/honey
honey web --listen 127.0.0.1:8765 --config ~/.config/honey/config.yaml
```

Optional flags:

| Flag | Purpose |
|------|---------|
| `--listen` | Host:port (must be loopback; default `127.0.0.1:8765`) |
| `--config` | Honey YAML path (same resolution as `honey search`) |
| `--record-dir` | Directory to store **SSH/K8s terminal session recordings** (enables replay in the UI when set) |
| `--files-root` | Local filesystem root for the file browser (default: `$HONEY_FILES_ROOT` or `$HOME`) |
| `--agent-bin` | Explicit path to the `honey-transfer-agent` binary (optional) |
| `--agent-build-cache-dir` | Cache directory when the server auto-builds the transfer agent |

On startup, Honey prints the URL and auth hints on stderr:

```text
Honey Web UI (Ctrl+C to stop)
  URL:   http://127.0.0.1:8765/?token=...
  API:   Authorization: Bearer <token>  or  X-Honey-Token: <token>
  WS:    /ws/ssh?token=<token>
  Assist: OPENAI_API_KEY (+ optional OPENAI_BASE_URL)
```

Open the **URL** in your browser (the query string includes the token).

### Authentication

- **Default:** a random hex token is generated each run.
- **Fixed token:** set `HONEY_WEB_TOKEN` in the environment before starting `honey web`.
- Pass the token as `?token=…`, or header `Authorization: Bearer <token>`, or `X-Honey-Token: <token>`.

## Features

### Backends, search, and config

- **Providers & backends:** Filters match the CLI; structured CRUD for `backends.*` mirrors the YAML file.
- **Search:** Same discovery as `honey search`, with results in the table.
- **Config:** Raw YAML editor plus schema-backed backend forms and `GET /api/v1/config/schema` for validation.

### Browser terminal

- WebSocket **`GET /ws/ssh?token=…`**: interactive session to **SSH hosts** (system `ssh` behavior) and **Kubernetes pods** (ephemeral exec TTY), aligned with the TUI.
- Optional **session recording** when `--record-dir` is set; recordings can be listed and replayed from the UI.

### Files and transfer

- Browse **local** paths under `--files-root` and **remote** paths on connected hosts.
- **Agent-based transfer** (`honey-transfer-agent`): copies between local, remote, and cloud storage using the separate agent binary (paths via `--agent-bin` / build cache).

**Prebuilt agents (no local checkout):** CI publishes `honey-transfer-agent-<goos>-<goarch>` assets (see `.github/workflows/honey-transfer-agent.yml`), built static then **compressed with UPX** (`--best --lzma`). When a local `go build` is not possible or fails, Honey **downloads** a prebuilt binary. **No env is required by default:** the URL uses the **same release tag as the running `honey` binary** (the link-time version from `honey --version`):

`https://github.com/shareed2k/honey/releases/download/<vTAG>/honey-transfer-agent-<goos>-<goarch>`

If the embedded version is empty or `0.0.0-dev`, Honey falls back to **`…/releases/latest/download/…`** so local builds still work.

| Variable | Description |
|----------|-------------|
| `HONEY_TRANSFER_AGENT_DOWNLOAD_BASE` | Optional override: base URL with **no** trailing slash (e.g. a pinned tag `…/releases/download/v1.2.3` or a mirror). Honey appends `/honey-transfer-agent-<goos>-<goarch>`. |
| `HONEY_TRANSFER_AGENT_DOWNLOAD_URL` | Optional **full URL template** (wins over base). Placeholders: `{os}`, `{arch}`, `{GOOS}`, `{GOARCH}`. |
| `HONEY_TRANSFER_AGENT_DOWNLOAD_DISABLE_DEFAULT` | Set to `1` / `true` to **turn off** the default GitHub-latest URL (e.g. air-gapped); then you must supply `DOWNLOAD_BASE` / `DOWNLOAD_URL` or `HONEY_TRANSFER_AGENT`. |

Download runs **after** a failed local build (or when the module root cannot be found). It is **not** used when `upx` packing is enabled for the agent cache (`HONEY_TRANSFER_AGENT_DISABLE_UPX` unset and `upx` on `PATH`). You can still set **`HONEY_TRANSFER_AGENT`** to a local binary to skip both build and download.

#### Presigned-URL transfer path

By default, `honey` orchestrates host↔host file transfers via cloud staging using
presigned S3/GCS URLs and `curl` on each remote, with the Go `honey-transfer-agent`
binary kept as a fallback. This eliminates the per-transfer upload of the agent
binary on hosts that have `curl`, `dd`, and `awk` installed.

**Config:**

```yaml
transfer:
  presigned_max_size: 5GiB           # files above this fall back to the agent path
  multipart_threshold: 64MiB         # single PUT below, multipart above
  presigned_url_ttl: 1h              # URL validity window (clamped 5m..24h)
  presigned_retry_with_agent: true   # transparent fallback on curl failure
  force_agent_path: false            # set true to disable the curl path globally
```

**Recommended bucket lifecycle rule:** the curl path tries to `DeleteObject`
after each transfer, but lifecycle rules catch any stragglers from operator
crashes:

```bash
# S3: expire the honey-transfer/ prefix after 24h
aws s3api put-bucket-lifecycle-configuration \
    --bucket "<bucket>" \
    --lifecycle-configuration '{"Rules":[{
        "ID":"honey-transfer-staging",
        "Status":"Enabled",
        "Filter":{"Prefix":"honey-transfer/"},
        "Expiration":{"Days":1}
    }]}'
```

**Fallback conditions:** the curl path is bypassed (agent path used instead) when:

- `force_agent_path: true` is set,
- the source or destination host is missing `curl`, `dd`, or `awk`,
- the file size exceeds `presigned_max_size`,
- the cloud SDK fails to sign a URL (e.g., GCS without a service-account key file).

### CUE recipes

- Open and run recipes from the UI (same semantics as `honey cue-exec`), including optional **`agent_transfer`** steps (A→cloud→B); the server passes its configured honey YAML path for `cloud_backend_ref` signing, like **`POST /api/v1/files/agent-transfer`**.
- **Recipe assist** (AI): explains or debugs a recipe using the file content plus optional dry-run output for the current host selection (requires `OPENAI_API_KEY`).

### Recipes (WebUI)

The WebUI's "recipes" tab walks you through running a CUE recipe in 4 steps:

1. Pick hosts (pre-filled from the Search tab; editable).
2. Pick a recipe from disk, a saved in-browser draft, or a recent run.
3. Review the resolved plan; optionally edit step commands / `run_as` / env in the structured Edit tab and save as a named draft. Drafts live in `localStorage` and never write back to `.cue` files on disk.
4. Execute and watch live per-host transcripts.

Recent runs are derived from session recordings, so enabling `record_session`
keeps the recent-runs list populated.

## AI assist

Assist features call an **OpenAI-compatible** HTTP API using the official key and optional base URL.

### Setup

| Variable | Required | Description |
|----------|----------|-------------|
| `OPENAI_API_KEY` | **Yes** for assist | API key (or token) for the provider. |
| `OPENAI_BASE_URL` | No | Override API base URL (e.g. local [LM Studio](https://lmstudio.ai/), Azure OpenAI-compatible gateway). If unset, the default OpenAI endpoint is used. |

After setting the key, restart `honey web`. **`GET /api/v1/meta`** includes `"terminal_assist_available": true` when Assist is configured.

**Models:** the UI loads chat models from **`GET /api/v1/terminal-assist/models`** (provider `ListModels`, cached briefly). You must pick a model **returned by that endpoint** for completions.

### What Assist does

1. **Terminal assist** — `POST /api/v1/terminal-assist`  
   Sends your question plus **terminal scrollback** (tail) to the model with a short system prompt aimed at shell/errors/next steps. Useful when you are stuck in an SSH or pod session.

2. **Recipe assist** — `POST /api/v1/recipes/assist`  
   Sends the recipe CUE source, parse/validation notes, and **dry-run plan text** (when hosts are selected) so the model can explain steps, risks, and fixes. It does not execute commands.

### Limits and tuning (environment)

| Variable | Default | Meaning |
|----------|---------|---------|
| `TERMINAL_ASSIST_MAX_SCROLLBACK_RUNES` | `24000` | Max Unicode runes of scrollback tail sent upstream. |
| `TERMINAL_ASSIST_MAX_USER_RUNES` | `4000` | Max runes for the user question. |
| `TERMINAL_ASSIST_RPM` | `30` | Max Assist requests **per client IP per minute** (sliding window). |
| `TERMINAL_ASSIST_MAX_TOKENS` | `1024` | Max completion tokens. |
| `TERMINAL_ASSIST_UPSTREAM_SEC` | `90` | Upstream request timeout (seconds). |

### Privacy and security

- Assist sends **scrollback and prompts to the configured API**. Do not paste secrets; rotate credentials if they may have leaked into a session.
- Loopback binding and bearer token reduce exposure but **do not encrypt** traffic to third-party AI providers—use only keys and endpoints you trust.

## API reference

Authenticate all routes below except static files: `Authorization: Bearer <token>` or `X-Honey-Token: <token>` (or `?token=` for GET).

**OpenAPI 3:** `GET /api/v1/openapi.json` returns the machine-readable spec for the REST surface (same auth). In the web UI, open the **API** tab (or `?tab=api-docs`) for an embedded **Swagger UI** explorer, including “Try it out” with your session token. Regenerate from the repo with `make openapi` (runs `go generate` in `internal/webserver`).

### Meta and discovery

- `GET /api/v1/meta` — version, config path, `session_recording_available`, `terminal_assist_available`.
- `GET /api/v1/providers` — search provider IDs (e.g. `k8s`).
- `GET /api/v1/backends` — configured backends.

### Search and exec

- `POST /api/v1/search` — search hosts (JSON body mirrors CLI search input).
- `POST /api/v1/exec` — remote execution from the UI.
- `POST /api/v1/cue-exec` — run CUE recipes.

### Config

- `GET` / `PUT /api/v1/config` — raw YAML.
- `GET /api/v1/config/schema` — schema for forms and lint.
- `GET /api/v1/config/backends`, `POST /api/v1/config/backends/{kind}`, `PUT`/`DELETE` with index — structured backend CRUD.

### Files and uploads

- `POST /api/v1/upload` — SFTP-style upload (drag-and-drop in UI).
- `POST /api/v1/files/local/list` — list under local root.
- `POST /api/v1/files/remote/list` — list on remote host.
- `POST /api/v1/files/copy` — copy between locations.
- `POST /api/v1/files/agent-transfer` — cloud/agent pipeline.

### Recipes

- `GET /api/v1/recipes` — list known recipe files (server allowlist).
- `POST /api/v1/recipes/view` — read/validate recipe content.
- `POST /api/v1/recipes/assist` — **recipe assist** (requires `OPENAI_API_KEY`).

### Recordings

- `GET /api/v1/recordings` — list recordings when `--record-dir` is set.
- `POST /api/v1/recordings/play` — fetch payload for replay.

### Terminal WebSocket and AI

- `GET /ws/ssh?token=…` — browser terminal (not JSON).
- `GET /api/v1/terminal-assist/models` — model IDs for Assist.
- `POST /api/v1/terminal-assist` — **terminal assist** (requires key; JSON body: `user_prompt`, `scrollback`, optional `max_lines`, `model`).

## Local UI development

The frontend lives in `webui` (Vite + React).

1. Start the Go server:

   ```bash
   honey web --listen 127.0.0.1:8765 --config ~/.config/honey/config.yaml
   ```

2. In another terminal:

   ```bash
   cd webui
   npm install
   npm run dev
   ```

3. Open the dev URL (typically `http://localhost:5173`). Vite proxies API/WebSocket to the Go backend.

For production, build assets into the embed tree:

```bash
make webui
```

Then rebuild `honey` so `internal/webserver/static` is included.
